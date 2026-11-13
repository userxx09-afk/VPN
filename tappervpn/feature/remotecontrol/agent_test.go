// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"tappervpn.com/types/logger"
)

// TestAgentProxy drives the daemon-side helperHostAgent over a pipe while a
// fake helper on the other end answers like the real user-session helper would,
// exercising info, consent, capture (helper->daemon frames), and input
// (daemon->helper).
func TestAgentProxy(t *testing.T) {
	daemonConn, helperConn := net.Pipe()

	// Daemon side: serve the agent link; it registers the proxy HostAgent.
	serveErr := make(chan error, 1)
	go func() { serveErr <- ServeAgentLink(daemonConn, logger.Discard) }()

	helper := newFrameConn(helperConn)

	// Helper announces itself.
	sendAgent(t, helper, AgentMessage{T: "info", Kind: "windows", Codecs: []string{"jpeg-tiles"}, Width: 1920, Height: 1080})

	// Wait for the proxy to register and absorb the info.
	waitFor(t, "host agent registered", func() bool {
		a := getHostAgent()
		return a != nil && a.Screen() == ScreenSize{1920, 1080}
	})
	agent := getHostAgent()
	defer RegisterHostAgent(nil)
	if agent.Kind() != "windows" || len(agent.Codecs()) != 1 {
		t.Fatalf("info not applied: kind=%q codecs=%v", agent.Kind(), agent.Codecs())
	}

	// Consent: daemon asks, helper allows.
	consentDone := make(chan ConsentDecision, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		consentDone <- agent.RequestConsent(ctx, ConsentRequest{PeerName: "phone", PeerID: "n1"})
	}()
	if m := recvAgent(t, helper); m.T != "consent" || m.PeerName != "phone" {
		t.Fatalf("expected consent request, got %+v", m)
	}
	sendAgent(t, helper, AgentMessage{T: "consent-reply", Allow: true})
	if d := <-consentDone; !d.Allow {
		t.Fatal("consent should have been allowed")
	}

	// Capture: daemon starts, helper streams a frame back. StartCapture writes
	// to the (synchronous) pipe, so run it off the goroutine that reads it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type capRes struct {
		ch  <-chan []byte
		err error
	}
	capCh := make(chan capRes, 1)
	go func() {
		f, e := agent.StartCapture(ctx, CodecJPEGTiles)
		capCh <- capRes{f, e}
	}()
	if m := recvAgent(t, helper); m.T != "capture-start" {
		t.Fatalf("expected capture-start, got %+v", m)
	}
	cap := <-capCh
	if cap.err != nil {
		t.Fatal(cap.err)
	}
	helper.write(ChVideo, []byte("JPEGDATA"))
	select {
	case f := <-cap.ch:
		if string(f) != "JPEGDATA" {
			t.Errorf("frame = %q", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame proxied from helper")
	}

	// Input: daemon injects (a pipe write), helper receives it on ChInput.
	go agent.InjectInput(InputEvent{Kind: InputNav, Button: uint8(NavHome)})
	ch, payload, err := helper.read()
	if err != nil {
		t.Fatal(err)
	}
	if ch != ChInput {
		t.Fatalf("expected input channel, got %d", ch)
	}
	e, err := DecodeInput(payload)
	if err != nil || e.Kind != InputNav || e.Button != uint8(NavHome) {
		t.Fatalf("bad injected input: %+v err=%v", e, err)
	}

	// Closing the helper ends the link.
	helperConn.Close()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeAgentLink did not return after close")
	}
}

func sendAgent(t *testing.T, fc *frameConn, m AgentMessage) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := fc.write(ChControl, b); err != nil {
		t.Fatal(err)
	}
}

func recvAgent(t *testing.T, fc *frameConn) AgentMessage {
	t.Helper()
	ch, payload, err := fc.read()
	if err != nil {
		t.Fatal(err)
	}
	if ch != ChControl {
		t.Fatalf("expected control frame, got channel %d", ch)
	}
	var m AgentMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
