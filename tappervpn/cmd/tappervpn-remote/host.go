// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Command tappervpn-remote is the user-session helper for TapperVPN's remote
// control feature. The tappervpnd service runs in session 0 and cannot capture
// the desktop, inject input, or show UI, so this helper runs in the interactive
// user session and does those jobs, attaching to the daemon over the local API.
//
// This file is the platform-neutral driver: it speaks the agent protocol
// (feature/remotecontrol) to the daemon and dispatches to a HostPlatform
// implementation (platform_windows.go for the real capture/input/consent, or a
// stub elsewhere).
package main

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	rc "tappervpn.com/feature/remotecontrol"
)

// HostPlatform is the OS-specific half of the host helper.
type HostPlatform interface {
	// Kind is "windows" (or another host platform name).
	Kind() string
	// Codecs lists the video codecs this platform can produce, best first.
	Codecs() []rc.Codec
	// Screen returns the current screen size in pixels.
	Screen() rc.ScreenSize
	// RequestConsent shows the consent prompt and blocks until the user decides
	// or timeoutSeconds elapses; returns true to allow.
	RequestConsent(peerName, peerID string, timeoutSeconds int) bool
	// StartCapture captures the screen, calling sink with each encoded frame,
	// until ctx is cancelled.
	StartCapture(ctx context.Context, codec rc.Codec, sink func([]byte)) error
	// InjectInput applies one input event.
	InjectInput(e rc.InputEvent)
	// SessionStart/SessionEnd bracket a live session for the indicator/kill UI.
	SessionStart(peerName string)
	SessionEnd()
}

type linkWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *linkWriter) frame(ch rc.Channel, payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return rc.WriteFrame(l.w, ch, payload)
}

func (l *linkWriter) control(m rc.AgentMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return l.frame(rc.ChControl, b)
}

// runHostHelper drives the helper's link to the daemon until it closes or ctx
// ends: it serves host requests (consent/capture/input) and launches a
// controller session when the daemon asks (control-start).
func runHostHelper(ctx context.Context, link io.ReadWriter, p Platform, logf func(string, ...any)) error {
	lw := &linkWriter{w: link}

	// Announce capabilities first; the daemon waits for this before advertising
	// the node as controllable.
	codecs := make([]string, 0, len(p.Codecs()))
	for _, c := range p.Codecs() {
		codecs = append(codecs, string(c))
	}
	s := p.Screen()
	if err := lw.control(rc.AgentMessage{T: "info", Kind: p.Kind(), Codecs: codecs, Width: s.Width, Height: s.Height}); err != nil {
		return err
	}

	var captureCancel context.CancelFunc
	stopCapture := func() {
		if captureCancel != nil {
			captureCancel()
			captureCancel = nil
		}
	}
	defer stopCapture()

	for {
		ch, payload, err := rc.ReadFrame(link)
		if err != nil {
			return err
		}
		switch ch {
		case rc.ChInput:
			e, err := rc.DecodeInput(payload)
			if err != nil {
				logf("bad input event: %v", err)
				continue
			}
			p.InjectInput(e)
		case rc.ChControl:
			var m rc.AgentMessage
			if err := json.Unmarshal(payload, &m); err != nil {
				logf("bad agent message: %v", err)
				continue
			}
			switch m.T {
			case "consent":
				// Prompt off the read loop so frames/input aren't blocked.
				go func(req rc.AgentMessage) {
					allow := p.RequestConsent(req.PeerName, req.PeerID, 60)
					if err := lw.control(rc.AgentMessage{T: "consent-reply", Allow: allow}); err != nil {
						logf("consent reply: %v", err)
					}
				}(m)
			case "capture-start":
				stopCapture()
				cctx, cancel := context.WithCancel(ctx)
				captureCancel = cancel
				codec := rc.Codec(m.Codec)
				go func() {
					if err := p.StartCapture(cctx, codec, func(f []byte) { lw.frame(rc.ChVideo, f) }); err != nil {
						logf("capture: %v", err)
					}
				}()
			case "capture-stop":
				stopCapture()
			case "session-start":
				p.SessionStart(m.PeerName)
			case "session-end":
				p.SessionEnd()
			case "control-start":
				go startControlling(ctx, m.PeerID, m.PeerName, p, logf)
			}
		}
	}
}
