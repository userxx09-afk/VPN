// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"tappervpn.com/types/logger"
)

// currentHelper is the attached user-session helper, if any, so the controller
// side (serveConnect) can ask it to start controlling a peer. There is at most
// one helper per machine.
var currentHelper atomic.Pointer[helperHostAgent]

// attachedHelper returns the currently attached helper, or nil.
func attachedHelper() *helperHostAgent { return currentHelper.Load() }

// On Windows (and any platform where the capturing/injecting code must run in a
// user session separate from the tappervpnd service), the daemon itself cannot
// capture the screen, inject input, or show a consent dialog. Instead a small
// user-session helper process attaches to the daemon and performs those jobs.
//
// This file is the daemon side of that split. When a helper attaches (via the
// remotecontrol/agent LocalAPI endpoint, see localapi.go), the daemon registers
// a HostAgent that proxies every HostAgent call to the helper over a single
// link, reusing the very same frame protocol used on the wire:
//
//	ChControl  JSON AgentMessage      both directions (info, consent, capture, session)
//	ChInput    encoded InputEvent daemon -> helper (input to inject)
//	ChVideo    encoded frame      helper -> daemon (captured screen)
//
// The daemon then runs the normal host session (RunHostSession) against the
// remote peer, with this proxy as its agent; captured frames flow
// helper->daemon->peer and input flows peer->daemon->helper. The hop between
// daemon and helper is a local pipe, so it is cheap.

// AgentMessage is the JSON carried on the ChControl channel of the helper link.
type AgentMessage struct {
	T string `json:"t"` // "info" | "consent" | "consent-reply" | "capture-start" | "capture-stop" | "capture-error" | "session-start" | "session-end" | "end"

	// info
	Kind   string   `json:"kind,omitempty"`
	Codecs []string `json:"codecs,omitempty"`
	Width  int      `json:"w,omitempty"`
	Height int      `json:"h,omitempty"`

	// consent
	PeerName string `json:"peer,omitempty"`
	PeerID   string `json:"peerID,omitempty"`
	Allow    bool   `json:"allow,omitempty"`

	// capture
	Codec string `json:"codec,omitempty"`

	// capture-error
	Error string `json:"error,omitempty"`
}

// helperHostAgent implements HostAgent by talking to the attached helper over
// the link. One helper serves at most one session at a time, which is
// sufficient for the UI.
type helperHostAgent struct {
	link *frameConn
	logf logger.Logf

	mu          sync.Mutex
	kind        string
	codecs      []Codec
	screen      ScreenSize
	consentWait chan bool
	captureCh   chan []byte
	captureErr  string
	sessionStop func()
}

func (a *helperHostAgent) Kind() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kind
}

func (a *helperHostAgent) Codecs() []Codec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Codec(nil), a.codecs...)
}

func (a *helperHostAgent) Screen() ScreenSize {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.screen
}

func (a *helperHostAgent) RequestConsent(ctx context.Context, req ConsentRequest) ConsentDecision {
	reply := make(chan bool, 1)
	a.mu.Lock()
	a.consentWait = reply
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.consentWait = nil
		a.mu.Unlock()
	}()

	if err := a.send(AgentMessage{T: "consent", PeerName: req.PeerName, PeerID: req.PeerID}); err != nil {
		return ConsentDecision{Allow: false}
	}
	select {
	case <-ctx.Done():
		return ConsentDecision{Allow: false}
	case allow := <-reply:
		return ConsentDecision{Allow: allow}
	}
}

func (a *helperHostAgent) StartCapture(ctx context.Context, codec Codec) (<-chan []byte, error) {
	ch := make(chan []byte, 8)
	a.mu.Lock()
	a.captureCh = ch
	a.captureErr = ""
	a.mu.Unlock()
	if err := a.send(AgentMessage{T: "capture-start", Codec: string(codec)}); err != nil {
		a.mu.Lock()
		a.captureCh = nil
		a.mu.Unlock()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		a.send(AgentMessage{T: "capture-stop"})
		a.mu.Lock()
		if a.captureCh == ch {
			close(ch)
			a.captureCh = nil
		}
		a.mu.Unlock()
	}()
	return ch, nil
}

// CaptureError reports why the helper's last capture failed, if it did.
func (a *helperHostAgent) CaptureError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.captureErr
}

func (a *helperHostAgent) InjectInput(e InputEvent) {
	a.link.write(ChInput, EncodeInput(e))
}

func (a *helperHostAgent) SessionStarted(req ConsentRequest, stop func()) {
	a.mu.Lock()
	a.sessionStop = stop
	a.mu.Unlock()
	a.send(AgentMessage{T: "session-start", PeerName: req.PeerName})
}

func (a *helperHostAgent) SessionEnded(req ConsentRequest) {
	a.mu.Lock()
	a.sessionStop = nil
	a.mu.Unlock()
	a.send(AgentMessage{T: "session-end"})
}

// StartControlling asks the helper to open a controller session to the named
// peer (it will dial back via the remotecontrol/dial endpoint and run the
// session with its own viewer window). Used on Windows, where the controller
// viewer lives in the helper rather than the daemon.
func (a *helperHostAgent) StartControlling(peerID, peerName string) error {
	return a.send(AgentMessage{T: "control-start", PeerID: peerID, PeerName: peerName})
}

func (a *helperHostAgent) send(m AgentMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return a.link.write(ChControl, b)
}

// readLoop dispatches frames arriving from the helper until the link closes.
func (a *helperHostAgent) readLoop() error {
	for {
		ch, payload, err := a.link.read()
		if err != nil {
			return err
		}
		switch ch {
		case ChVideo:
			a.mu.Lock()
			cap := a.captureCh
			a.mu.Unlock()
			if cap != nil {
				select {
				case cap <- payload:
				default: // drop if the peer link is backpressured
				}
			}
		case ChControl:
			var m AgentMessage
			if err := json.Unmarshal(payload, &m); err != nil {
				a.logf("remotecontrol: bad agent message: %v", err)
				continue
			}
			a.handleControl(m)
		}
	}
}

func (a *helperHostAgent) handleControl(m AgentMessage) {
	switch m.T {
	case "info":
		a.mu.Lock()
		a.kind = m.Kind
		a.codecs = a.codecs[:0]
		for _, c := range m.Codecs {
			a.codecs = append(a.codecs, Codec(c))
		}
		a.screen = ScreenSize{Width: m.Width, Height: m.Height}
		a.mu.Unlock()
	case "consent-reply":
		a.mu.Lock()
		w := a.consentWait
		a.mu.Unlock()
		if w != nil {
			select {
			case w <- m.Allow:
			default:
			}
		}
	case "capture-error": // the helper couldn't capture; end the stream
		a.mu.Lock()
		a.captureErr = m.Error
		if a.captureCh != nil {
			close(a.captureCh)
			a.captureCh = nil
		}
		a.mu.Unlock()
	case "end": // helper's kill switch
		a.mu.Lock()
		stop := a.sessionStop
		a.mu.Unlock()
		if stop != nil {
			stop()
		}
	}
}

// ServeAgentLink runs the daemon side of a helper link on rw until it closes.
// It waits for the helper's initial "info" message, registers a HostAgent that
// proxies to the helper for the link's lifetime, and unregisters on exit. Only
// one helper is active at a time; a second replaces the first.
func ServeAgentLink(rw io.ReadWriter, logf logger.Logf) error {
	a := &helperHostAgent{link: newFrameConn(rw), logf: logf}

	// The first frame must be the info message so we know the host's
	// capabilities before advertising ourselves as controllable.
	ch, payload, err := a.link.read()
	if err != nil {
		return fmt.Errorf("reading helper info: %w", err)
	}
	if ch != ChControl {
		return fmt.Errorf("expected info on control channel, got %d", ch)
	}
	var info AgentMessage
	if err := json.Unmarshal(payload, &info); err != nil || info.T != "info" {
		return fmt.Errorf("malformed helper info: %v", err)
	}
	a.handleControl(info)

	RegisterHostAgent(a)
	defer func() {
		// Only clear it if a newer helper hasn't taken over meanwhile.
		platformMu.Lock()
		if hostAgent == HostAgent(a) {
			hostAgent = nil
		}
		platformMu.Unlock()
	}()
	currentHelper.Store(a)
	defer currentHelper.CompareAndSwap(a, nil)
	logf("remotecontrol: helper attached (kind=%s, %dx%d)", a.Kind(), a.screen.Width, a.screen.Height)

	return a.readLoop()
}
