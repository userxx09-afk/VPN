// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"

	"tappervpn.com/feature/remotecontrol"
)

// This file bridges the platform-neutral feature/remotecontrol package to the
// Android app. The feature package owns the mesh transport, consent handshake,
// and session loops; here we adapt its HostAgent/Viewer hooks to callbacks
// implemented in Kotlin (screen capture via MediaProjection, input via an
// AccessibilityService, the consent dialog, and the controller viewer).
//
// There is a single Application per process, so the active host-capture and
// controller-input handles are kept in one package-level struct guarded by a
// mutex. At most one host session and one controller session are supported at
// a time, which is sufficient for the app's UI.

const rcConsentTimeoutSeconds = 60

// RemoteControlHost is implemented in Kotlin. It supplies everything the host
// (controlled) side needs: the consent prompt, screen info, capture control,
// and input injection. All methods may be called from Go goroutines.
type RemoteControlHost interface {
	// Kind identifies the platform: "android" or "firetv".
	Kind() string
	// Codecs is a comma-separated list of video codecs this host can produce,
	// best first (e.g. "h264,jpeg-tiles").
	Codecs() string
	// ScreenWidth and ScreenHeight return the current screen size in pixels.
	// (int32, not int, so gomobile maps it to Kotlin Int rather than Long.)
	ScreenWidth() int32
	ScreenHeight() int32
	// RequestConsent shows the "Allow <peerName> to control this device?"
	// prompt and blocks until the user decides or timeoutSeconds elapses.
	// It returns true to allow.
	RequestConsent(peerName string, peerID string, timeoutSeconds int32) bool
	// StartCapture begins producing encoded frames in the named codec; the
	// Kotlin side delivers each frame by calling App.PushRemoteControlFrame.
	StartCapture(codec string) error
	// StopCapture stops the capture started by StartCapture.
	StopCapture()
	// InjectInput applies one input event to the device. kind/button/code are
	// the numeric values from the feature's input protocol.
	InjectInput(kind int32, x int32, y int32, button int32, down bool, code int32, text string)
	// SessionStarted/SessionEnded bracket a live session so Kotlin can show and
	// hide the "being controlled by <peerName>" notification and kill switch.
	SessionStarted(peerName string)
	SessionEnded(peerName string)
}

// RemoteControlViewer is implemented in Kotlin for the controller side. It
// renders frames the host sends and is told the negotiated format on Start.
type RemoteControlViewer interface {
	Start(codec string, width int32, height int32, hostKind string) error
	Frame(frame []byte)
	Resize(width int32, height int32)
	Close()
}

var rc struct {
	mu sync.Mutex

	// captureCh receives encoded frames pushed from Kotlin for the active host
	// capture; nil when no capture is running.
	captureCh chan []byte
	// hostStop ends the active host session (set between SessionStarted and
	// SessionEnded).
	hostStop func()

	// viewerInput delivers controller input events to the active controller
	// session; nil when none is running. ctrlCancel ends that session.
	viewerInput chan remotecontrol.InputEvent
	ctrlCancel  func()
}

// SetRemoteControlHost installs the Kotlin host implementation, making this
// device controllable (subject to per-session consent). Pass once at startup.
func (app *App) SetRemoteControlHost(h RemoteControlHost) {
	remotecontrol.RegisterHostAgent(&rcHostAgent{cb: h})
}

// PushRemoteControlFrame delivers one encoded video frame from the Kotlin
// capturer to the active host session. Frames are dropped if the session is
// momentarily backpressured, so a slow link degrades frame rate rather than
// blocking the encoder.
func (app *App) PushRemoteControlFrame(frame []byte) {
	rc.mu.Lock()
	ch := rc.captureCh
	rc.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- frame:
	default:
	}
}

// ConnectRemoteControl asks the peer with the given StableNodeID to accept a
// control session and, on acceptance, drives the supplied viewer until the
// session ends. It blocks for the session's lifetime, so Kotlin must call it
// off the main thread. It returns an error if the peer declines or is
// unreachable.
func (app *App) ConnectRemoteControl(stableID string, v RemoteControlViewer) error {
	app.backendMu.Lock()
	b := app.backend
	app.backendMu.Unlock()
	if b == nil {
		return errors.New("backend not ready")
	}

	input := make(chan remotecontrol.InputEvent, 32)
	ctx, cancel := context.WithCancel(context.Background())

	rc.mu.Lock()
	// End any previous controller session before starting a new one.
	if rc.ctrlCancel != nil {
		rc.ctrlCancel()
	}
	rc.viewerInput = input
	rc.ctrlCancel = cancel
	rc.mu.Unlock()

	err := remotecontrol.ConnectWithViewer(ctx, b, stableID, &rcViewer{cb: v, input: input}, log.Printf)

	rc.mu.Lock()
	if rc.viewerInput == input {
		rc.viewerInput = nil
		rc.ctrlCancel = nil
	}
	rc.mu.Unlock()
	cancel()
	return err
}

// SendRemoteControlInput forwards a user input event from the Kotlin viewer to
// the active controller session.
func (app *App) SendRemoteControlInput(kind, x, y, button int32, down bool, code int32, text string) {
	rc.mu.Lock()
	ch := rc.viewerInput
	rc.mu.Unlock()
	if ch == nil {
		return
	}
	e := remotecontrol.InputEvent{
		Kind:   remotecontrol.InputKind(kind),
		X:      x,
		Y:      y,
		Button: uint8(button),
		Down:   down,
		Code:   uint32(code),
		Text:   text,
	}
	select {
	case ch <- e:
	default:
	}
}

// StopRemoteControl ends any active host and/or controller session on this
// device (the user's kill switch).
func (app *App) StopRemoteControl() {
	rc.mu.Lock()
	hs, cc := rc.hostStop, rc.ctrlCancel
	rc.mu.Unlock()
	if hs != nil {
		hs()
	}
	if cc != nil {
		cc()
	}
}

// rcHostAgent adapts the Kotlin RemoteControlHost to remotecontrol.HostAgent.
type rcHostAgent struct {
	cb RemoteControlHost
}

func (h *rcHostAgent) Kind() string { return h.cb.Kind() }

func (h *rcHostAgent) Codecs() []remotecontrol.Codec {
	var out []remotecontrol.Codec
	for _, s := range strings.Split(h.cb.Codecs(), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, remotecontrol.Codec(s))
		}
	}
	return out
}

func (h *rcHostAgent) Screen() remotecontrol.ScreenSize {
	return remotecontrol.ScreenSize{Width: int(h.cb.ScreenWidth()), Height: int(h.cb.ScreenHeight())}
}

func (h *rcHostAgent) RequestConsent(ctx context.Context, req remotecontrol.ConsentRequest) remotecontrol.ConsentDecision {
	res := make(chan bool, 1)
	go func() { res <- h.cb.RequestConsent(req.PeerName, req.PeerID, rcConsentTimeoutSeconds) }()
	select {
	case <-ctx.Done():
		return remotecontrol.ConsentDecision{Allow: false}
	case allow := <-res:
		return remotecontrol.ConsentDecision{Allow: allow}
	}
}

func (h *rcHostAgent) StartCapture(ctx context.Context, codec remotecontrol.Codec) (<-chan []byte, error) {
	ch := make(chan []byte, 8)
	rc.mu.Lock()
	rc.captureCh = ch
	rc.mu.Unlock()
	if err := h.cb.StartCapture(string(codec)); err != nil {
		rc.mu.Lock()
		if rc.captureCh == ch {
			rc.captureCh = nil
		}
		rc.mu.Unlock()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		h.cb.StopCapture()
		rc.mu.Lock()
		if rc.captureCh == ch {
			close(ch)
			rc.captureCh = nil
		}
		rc.mu.Unlock()
	}()
	return ch, nil
}

func (h *rcHostAgent) InjectInput(e remotecontrol.InputEvent) {
	h.cb.InjectInput(int32(e.Kind), e.X, e.Y, int32(e.Button), e.Down, int32(e.Code), e.Text)
}

func (h *rcHostAgent) SessionStarted(req remotecontrol.ConsentRequest, stop func()) {
	rc.mu.Lock()
	rc.hostStop = stop
	rc.mu.Unlock()
	h.cb.SessionStarted(req.PeerName)
}

func (h *rcHostAgent) SessionEnded(req remotecontrol.ConsentRequest) {
	rc.mu.Lock()
	rc.hostStop = nil
	rc.mu.Unlock()
	h.cb.SessionEnded(req.PeerName)
}

// rcViewer adapts the Kotlin RemoteControlViewer to remotecontrol.Viewer.
type rcViewer struct {
	cb    RemoteControlViewer
	input chan remotecontrol.InputEvent
}

func (v *rcViewer) Start(a remotecontrol.Accept) error {
	return v.cb.Start(string(a.Codec), int32(a.Screen.Width), int32(a.Screen.Height), a.HostKind)
}
func (v *rcViewer) Frame(payload []byte)                   { v.cb.Frame(payload) }
func (v *rcViewer) Resize(s remotecontrol.ScreenSize)      { v.cb.Resize(int32(s.Width), int32(s.Height)) }
func (v *rcViewer) Input() <-chan remotecontrol.InputEvent { return v.input }
func (v *rcViewer) Close()                                 { v.cb.Close() }
