// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"sync"
)

// This file defines the platform boundary. The shared package owns the
// transport, handshake, and session loops (session.go); it delegates the four
// machine-specific jobs — asking the user, capturing the screen, injecting
// input, and displaying a remote screen — to a platform implementation set at
// startup.
//
// On Windows the daemon runs in session 0 and cannot do any of these, so its
// HostAgent/Viewer are thin proxies that forward to the user-session helper
// over the local agent stream (see localapi.go, and cmd/tappervpn-remote). On
// Android the Go core runs inside the app process, so the implementations are
// JNI callbacks into Kotlin.
//
// The agent and viewer factory are process-global platform capabilities (they
// concern the one physical machine), not per-LocalBackend state; per-session
// bookkeeping lives on the extension instead (ext.go).

// ConsentRequest describes an incoming control request for the user to judge.
type ConsentRequest struct {
	// PeerName is the requesting device's name, for display.
	PeerName string
	// PeerID is the requester's stable node identity, used as the key for a
	// remembered "always allow" decision.
	PeerID string
}

// ConsentDecision is the user's answer to a ConsentRequest.
type ConsentDecision struct {
	// Allow reports whether the session may proceed.
	Allow bool
	// Remember, when set with Allow, asks to persist an "always allow this
	// device" decision so future requests from PeerID skip the prompt.
	Remember bool
}

// HostAgent is the host (controlled) side's platform implementation: it prompts
// for consent, reports and captures the screen, and injects input. All methods
// may be called from multiple goroutines.
type HostAgent interface {
	// Kind identifies the platform: "windows", "android", or "firetv".
	Kind() string

	// Codecs lists the video codecs this host can produce, best first.
	Codecs() []Codec

	// Screen returns the current screen size in pixels.
	Screen() ScreenSize

	// RequestConsent shows the consent prompt and blocks until the user decides
	// or ctx is done (timeout/cancel). A ctx-expired call must return a
	// non-Allow decision. It is only called after the request has already been
	// authenticated to come from PeerID over the mesh.
	RequestConsent(ctx context.Context, req ConsentRequest) ConsentDecision

	// StartCapture begins producing complete ChVideo payloads in codec until
	// ctx is cancelled. The channel is closed when capture stops.
	StartCapture(ctx context.Context, codec Codec) (<-chan []byte, error)

	// InjectInput applies a single input event to the host machine.
	InjectInput(e InputEvent)

	// SessionStarted/SessionEnded bracket a live session so the platform can
	// show and hide its "being controlled by PeerName" indicator and kill
	// switch. SessionEnded is always called once per SessionStarted.
	SessionStarted(req ConsentRequest, stop func())
	SessionEnded(req ConsentRequest)
}

// Viewer is the controller side's platform display. Start is called once the
// host accepts; Frame delivers video payloads; Input yields user-generated
// events to forward to the host.
type Viewer interface {
	Start(a Accept) error
	Frame(payload []byte)
	Resize(s ScreenSize)
	Input() <-chan InputEvent
	Close()
}

var (
	platformMu   sync.Mutex
	hostAgent    HostAgent
	viewerMaker  func() (Viewer, error)
	rememberFunc func(peerID string) bool // consult persisted "always allow"
	persistFunc  func(peerID string)      // persist "always allow"
)

// RegisterHostAgent installs the host-side platform implementation, making this
// node controllable (subject to consent). Called once at startup by the
// platform package; a nil agent leaves the node controller-only.
func RegisterHostAgent(a HostAgent) {
	platformMu.Lock()
	defer platformMu.Unlock()
	hostAgent = a
}

// RegisterViewerMaker installs the controller-side viewer factory, enabling
// this node to control others. Called once at startup by the platform package.
func RegisterViewerMaker(make func() (Viewer, error)) {
	platformMu.Lock()
	defer platformMu.Unlock()
	viewerMaker = make
}

// RegisterConsentMemory installs optional persistence for "always allow"
// decisions. remember reports whether peerID was previously always-allowed;
// persist records a new always-allow. Either may be nil.
func RegisterConsentMemory(remember func(peerID string) bool, persist func(peerID string)) {
	platformMu.Lock()
	defer platformMu.Unlock()
	rememberFunc, persistFunc = remember, persist
}

func getHostAgent() HostAgent {
	platformMu.Lock()
	defer platformMu.Unlock()
	return hostAgent
}

func getViewerMaker() func() (Viewer, error) {
	platformMu.Lock()
	defer platformMu.Unlock()
	return viewerMaker
}

// isRemembered reports whether peerID has a persisted always-allow decision.
func isRemembered(peerID string) bool {
	platformMu.Lock()
	f := rememberFunc
	platformMu.Unlock()
	return f != nil && f(peerID)
}

func rememberPeer(peerID string) {
	platformMu.Lock()
	f := persistFunc
	platformMu.Unlock()
	if f != nil {
		f(peerID)
	}
}

// HostCapable reports whether this node can be controlled (a host agent is
// installed). ControllerCapable reports whether it can control others.
func HostCapable() bool       { return getHostAgent() != nil }
func ControllerCapable() bool { return getViewerMaker() != nil }
