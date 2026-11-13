// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"net/url"

	rc "tappervpn.com/feature/remotecontrol"
)

const dialURL = "http://local-tappervpnd.sock/localapi/v0/remotecontrol/dial"

// ControllerPlatform is the OS-specific controller (viewer) half of the helper.
type ControllerPlatform interface {
	// NewViewer creates a window that renders the remote screen and reports the
	// user's input. The returned viewer is driven by the session and closed when
	// it ends.
	NewViewer(peerName string) (rc.Viewer, error)
}

// Platform is the full helper implementation: host and controller.
type Platform interface {
	HostPlatform
	ControllerPlatform
}

// startControlling opens a controller session to peerID. The daemon relays the
// connection to the peer (and the peer's user must accept first), then this runs
// the session against a local viewer window until it closes.
func startControlling(ctx context.Context, peerID, peerName string, p ControllerPlatform, logf func(string, ...any)) {
	viewer, err := p.NewViewer(peerName)
	if err != nil {
		logf("controller: viewer: %v", err)
		return
	}

	// Upgrade to a raw bidirectional connection; the daemon relays it to the
	// peer (whose user must accept first) and splices the two streams.
	conn, err := dialUpgrade(ctx, dialURL+"?id="+url.QueryEscape(peerID))
	if err != nil {
		viewer.Close()
		logf("controller: %s declined or unreachable: %v", peerName, err)
		return
	}
	defer conn.Close()

	// RunControllerSession closes the viewer when it returns.
	if err := rc.RunControllerSession(ctx, conn, viewer, logf); err != nil {
		logf("controller: session with %s ended: %v", peerName, err)
	}
}
