// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"tappervpn.com/types/logger"
)

// fakeAgent is an in-memory HostAgent for tests.
type fakeAgent struct {
	mu       sync.Mutex
	injected []InputEvent
	frames   chan []byte
	started  bool
	ended    bool
}

func newFakeAgent() *fakeAgent { return &fakeAgent{frames: make(chan []byte, 8)} }

func (a *fakeAgent) Kind() string       { return "test" }
func (a *fakeAgent) Codecs() []Codec    { return []Codec{CodecJPEGTiles} }
func (a *fakeAgent) Screen() ScreenSize { return ScreenSize{1280, 720} }
func (a *fakeAgent) RequestConsent(ctx context.Context, req ConsentRequest) ConsentDecision {
	return ConsentDecision{Allow: true}
}
func (a *fakeAgent) StartCapture(ctx context.Context, codec Codec) (<-chan []byte, error) {
	return a.frames, nil
}
func (a *fakeAgent) InjectInput(e InputEvent) {
	a.mu.Lock()
	a.injected = append(a.injected, e)
	a.mu.Unlock()
}
func (a *fakeAgent) SessionStarted(req ConsentRequest, stop func()) {
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
}
func (a *fakeAgent) SessionEnded(req ConsentRequest) {
	a.mu.Lock()
	a.ended = true
	a.mu.Unlock()
}

// fakeViewer is an in-memory Viewer for tests.
type fakeViewer struct {
	mu       sync.Mutex
	accepted *Accept
	frames   [][]byte
	input    chan InputEvent
	closed   bool
}

func newFakeViewer() *fakeViewer { return &fakeViewer{input: make(chan InputEvent, 8)} }

func (v *fakeViewer) Start(a Accept) error {
	v.mu.Lock()
	v.accepted = &a
	v.mu.Unlock()
	return nil
}
func (v *fakeViewer) Frame(p []byte) {
	v.mu.Lock()
	v.frames = append(v.frames, p)
	v.mu.Unlock()
}
func (v *fakeViewer) Resize(s ScreenSize)      {}
func (v *fakeViewer) Input() <-chan InputEvent { return v.input }
func (v *fakeViewer) Close() {
	v.mu.Lock()
	v.closed = true
	v.mu.Unlock()
}

// TestSessionEndToEnd wires a host and controller session over net.Pipe and
// checks the full handshake, a video frame host→controller, and an input event
// controller→host.
func TestSessionEndToEnd(t *testing.T) {
	hostConn, ctrlConn := net.Pipe()
	agent := newFakeAgent()
	viewer := newFakeViewer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hostDone := make(chan error, 1)
	ctrlDone := make(chan error, 1)
	go func() {
		hostDone <- RunHostSession(ctx, hostConn, agent, ConsentRequest{PeerName: "tester"}, logger.Discard)
	}()
	go func() {
		ctrlDone <- RunControllerSession(ctx, ctrlConn, viewer, logger.Discard)
	}()

	// Controller should receive Accept.
	waitFor(t, "viewer accept", func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return viewer.accepted != nil
	})
	viewer.mu.Lock()
	if viewer.accepted.Screen != (ScreenSize{1280, 720}) || viewer.accepted.Codec != CodecJPEGTiles {
		t.Errorf("bad accept: %+v", viewer.accepted)
	}
	viewer.mu.Unlock()

	// Host capture produces a frame; controller should render it.
	agent.frames <- []byte("FRAME-DATA")
	waitFor(t, "viewer frame", func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.frames) == 1 && string(viewer.frames[0]) == "FRAME-DATA"
	})

	// Controller user generates input; host should inject it.
	viewer.input <- InputEvent{Kind: InputNav, Button: uint8(NavOK)}
	waitFor(t, "input inject", func() bool {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return len(agent.injected) == 1 && agent.injected[0].Kind == InputNav
	})

	// Host session-started indicator must be up.
	agent.mu.Lock()
	if !agent.started {
		t.Error("agent.SessionStarted not called")
	}
	agent.mu.Unlock()

	// Tear down from the controller side (close capture -> both end).
	close(agent.frames)
	select {
	case <-hostDone:
	case <-time.After(3 * time.Second):
		t.Fatal("host session did not end")
	}
	_ = ctrlConn.Close()
	<-ctrlDone

	agent.mu.Lock()
	if !agent.ended {
		t.Error("agent.SessionEnded not called")
	}
	agent.mu.Unlock()
	viewer.mu.Lock()
	if !viewer.closed {
		t.Error("viewer not closed")
	}
	viewer.mu.Unlock()
}

// TestChooseCodec covers negotiation.
func TestChooseCodec(t *testing.T) {
	if c, ok := chooseCodec([]Codec{CodecH264, CodecJPEGTiles}, []Codec{CodecJPEGTiles}); !ok || c != CodecJPEGTiles {
		t.Errorf("got %q ok=%v, want jpeg-tiles", c, ok)
	}
	if _, ok := chooseCodec([]Codec{CodecH264}, []Codec{CodecJPEGTiles}); ok {
		t.Error("expected no common codec")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
