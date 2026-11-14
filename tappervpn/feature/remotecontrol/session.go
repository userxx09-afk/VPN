// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"tappervpn.com/types/logger"
)

// frameConn serializes frame writes on a single connection shared by multiple
// goroutines (the video pump and the control/stats writers, or the input pump
// and control writers on the controller side).
type frameConn struct {
	mu sync.Mutex
	w  io.Writer
	r  io.Reader
}

func newFrameConn(rw io.ReadWriter) *frameConn { return &frameConn{w: rw, r: rw} }

func (c *frameConn) write(ch Channel, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.w, ch, payload)
}

func (c *frameConn) writeControl(m ControlMessage) error {
	b, err := encodeControl(m)
	if err != nil {
		return err
	}
	return c.write(ChControl, b)
}

func (c *frameConn) read() (Channel, []byte, error) { return ReadFrame(c.r) }

// RunHostSession runs the controlled (host) side of a session on an already-
// consented, hijacked connection. It performs the handshake, starts capture,
// pumps video to the controller, and applies incoming input via agent. It
// returns when the connection closes, either side sends Bye, or ctx is done.
//
// Consent must already have been granted before this is called; see peerapi.go.
func RunHostSession(ctx context.Context, rw io.ReadWriter, agent HostAgent, req ConsentRequest, logf logger.Logf) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fc := newFrameConn(rw)

	// Read the controller's Hello.
	ch, payload, err := fc.read()
	if err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}
	if ch != ChControl {
		return fmt.Errorf("expected hello on control channel, got channel %d", ch)
	}
	msg, err := decodeControl(payload)
	if err != nil || msg.Type != CtHello || msg.Hello == nil {
		return fmt.Errorf("malformed hello: %v", err)
	}
	if msg.Hello.Version != ProtocolVersion {
		return fmt.Errorf("protocol version mismatch: controller=%d host=%d", msg.Hello.Version, ProtocolVersion)
	}

	codec, ok := chooseCodec(agent.Codecs(), msg.Hello.Codecs)
	if !ok {
		return fmt.Errorf("no common codec (host=%v controller=%v)", agent.Codecs(), msg.Hello.Codecs)
	}

	screen := agent.Screen()
	if err := fc.writeControl(ControlMessage{
		Type:   CtAccept,
		Accept: &Accept{Version: ProtocolVersion, Codec: codec, Screen: screen, HostKind: agent.Kind(), Note: viewOnlyReason(agent)},
	}); err != nil {
		return fmt.Errorf("sending accept: %w", err)
	}

	// Visible "being controlled" indicator + kill switch for the session's life.
	agent.SessionStarted(req, cancel)
	defer agent.SessionEnded(req)

	frames, err := agent.StartCapture(ctx, codec)
	if err != nil {
		_ = fc.writeControl(ControlMessage{Type: CtBye, Bye: &Bye{Reason: "The other device couldn't share its screen: " + err.Error()}})
		return fmt.Errorf("starting capture: %w", err)
	}

	// Reader goroutine: apply input, honor Bye, end on EOF.
	readErr := make(chan error, 1)
	go func() { readErr <- hostReadLoop(fc, agent, logf) }()

	// If capture fails silently (it runs in another process or service on most
	// platforms), no frame ever arrives. Rather than leave the controller
	// staring at an empty window, end the session and say why.
	firstFrame := time.NewTimer(firstFrameTimeout)
	defer firstFrame.Stop()
	firstFrameC := firstFrame.C

	// Writer loop: pump captured frames until capture stops or ctx ends.
	for {
		select {
		case <-ctx.Done():
			_ = fc.writeControl(ControlMessage{Type: CtBye, Bye: &Bye{Reason: "The other device ended the session."}})
			return ctx.Err()
		case err := <-readErr:
			return err
		case <-firstFrameC:
			_ = fc.writeControl(ControlMessage{Type: CtBye, Bye: &Bye{Reason: captureFailedReason(agent)}})
			return errors.New("no frames captured")
		case frame, ok := <-frames:
			if !ok {
				_ = fc.writeControl(ControlMessage{Type: CtBye, Bye: &Bye{Reason: captureFailedReason(agent)}})
				return nil
			}
			firstFrameC = nil
			if err := fc.write(ChVideo, frame); err != nil {
				return fmt.Errorf("writing video: %w", err)
			}
		}
	}
}

func hostReadLoop(fc *frameConn, agent HostAgent, logf logger.Logf) error {
	for {
		ch, payload, err := fc.read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch ch {
		case ChInput:
			e, err := DecodeInput(payload)
			if err != nil {
				logf("remotecontrol: bad input event: %v", err)
				continue
			}
			agent.InjectInput(e)
		case ChControl:
			msg, err := decodeControl(payload)
			if err != nil {
				logf("remotecontrol: bad control message: %v", err)
				continue
			}
			if msg.Type == CtBye {
				return nil
			}
		default:
			// Ignore unknown channels for forward compatibility.
		}
	}
}

// RunControllerSession runs the controlling side on an already-established
// (hijacked) connection to a host that will prompt for consent. It sends Hello,
// waits for Accept (the host returns an HTTP error before hijack on denial, so
// reaching here means the stream is live), drives viewer, and forwards input.
func RunControllerSession(ctx context.Context, rw io.ReadWriter, viewer Viewer, logf logger.Logf) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fc := newFrameConn(rw)

	if err := fc.writeControl(ControlMessage{
		Type:  CtHello,
		Hello: &Hello{Version: ProtocolVersion, Codecs: viewerCodecs()},
	}); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}

	// Await Accept.
	ch, payload, err := fc.read()
	if err != nil {
		return fmt.Errorf("awaiting accept: %w", err)
	}
	if ch != ChControl {
		return fmt.Errorf("expected accept on control channel, got %d", ch)
	}
	msg, err := decodeControl(payload)
	if err != nil || msg.Type != CtAccept || msg.Accept == nil {
		return fmt.Errorf("malformed accept: %v", err)
	}
	if err := viewer.Start(*msg.Accept); err != nil {
		return fmt.Errorf("starting viewer: %w", err)
	}
	defer viewer.Close()
	setViewerStatus(viewer, "Connected. Waiting for the picture…", false)
	if msg.Accept.Note != "" {
		if nv, ok := viewer.(NoticeViewer); ok {
			nv.Notice(msg.Accept.Note)
		}
	}

	// Input pump: user events from the viewer to the host.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-viewer.Input():
				if !ok {
					cancel() // viewer closed
					return
				}
				if err := fc.write(ChInput, EncodeInput(e)); err != nil {
					logf("remotecontrol: input write: %v", err)
					cancel()
					return
				}
			}
		}
	}()

	// Receive loop: video to the viewer, control housekeeping.
	for {
		if ctx.Err() != nil {
			_ = fc.writeControl(ControlMessage{Type: CtBye})
			return ctx.Err()
		}
		ch, payload, err := fc.read()
		if err != nil {
			if ctx.Err() == nil {
				setViewerStatus(viewer, "The connection to the other device was lost.", true)
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch ch {
		case ChVideo:
			viewer.Frame(payload)
		case ChControl:
			m, err := decodeControl(payload)
			if err != nil {
				logf("remotecontrol: bad control message: %v", err)
				continue
			}
			switch m.Type {
			case CtResize:
				if m.Resize != nil {
					viewer.Resize(*m.Resize)
				}
			case CtBye:
				reason := "The other device ended the session."
				if m.Bye != nil && m.Bye.Reason != "" {
					reason = m.Bye.Reason
				}
				setViewerStatus(viewer, reason, true)
				return nil
			}
		}
	}
}

// StatusViewer is optionally implemented by a Viewer that can show a line of
// text to the user (e.g. "Waiting for the picture…", or why a session ended).
// When final is true the session is over; the viewer should keep the message
// visible until the user dismisses it rather than vanishing on Close.
type StatusViewer interface {
	Status(msg string, final bool)
}

func setViewerStatus(v Viewer, msg string, final bool) {
	if sv, ok := v.(StatusViewer); ok {
		sv.Status(msg, final)
	}
}

// NoticeViewer is optionally implemented by a Viewer that can keep a short
// message visible for the whole session (see Accept.Note).
type NoticeViewer interface {
	Notice(msg string)
}

// ViewOnlyHost is optionally implemented by a HostAgent that can tell when it
// can be watched but not controlled; it returns why, or "" if input works.
type ViewOnlyHost interface {
	ViewOnly() string
}

func viewOnlyReason(agent HostAgent) string {
	if v, ok := agent.(ViewOnlyHost); ok {
		return v.ViewOnly()
	}
	return ""
}

// CaptureErrorer is optionally implemented by a HostAgent that knows why its
// capture failed, so the controller can be told something useful.
type CaptureErrorer interface {
	CaptureError() string
}

func captureFailedReason(agent HostAgent) string {
	if ce, ok := agent.(CaptureErrorer); ok {
		if msg := ce.CaptureError(); msg != "" {
			return "The other device couldn't share its screen: " + msg
		}
	}
	return "The other device accepted, but couldn't share its screen."
}

// firstFrameTimeout is how long a host waits for its first captured frame
// before giving up on the session as a capture failure.
var firstFrameTimeout = 15 * time.Second

// chooseCodec picks the first host codec that the controller also supports.
func chooseCodec(hostCodecs, controllerCodecs []Codec) (Codec, bool) {
	for _, hc := range hostCodecs {
		for _, cc := range controllerCodecs {
			if hc == cc {
				return hc, true
			}
		}
	}
	return "", false
}

// viewerCodecs is the controller's advertised decode capability. Every platform
// can decode JPEG tiles; H.264 is offered first where a decoder is wired in via
// the viewer (the viewer maker may override this in a future revision).
func viewerCodecs() []Codec { return []Codec{CodecMJPEG} }

// consentTimeout bounds how long a host waits for the user to answer a prompt
// before auto-denying, so a request cannot pin resources indefinitely.
const consentTimeout = 60 * time.Second
