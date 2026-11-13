// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"tappervpn.com/client/local"
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

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dialURL+"?id="+url.QueryEscape(peerID), pr)
	if err != nil {
		viewer.Close()
		pw.Close()
		logf("controller: request: %v", err)
		return
	}
	req.ContentLength = -1

	resp, err := (&local.Client{}).DoLocalRequest(req)
	if err != nil {
		viewer.Close()
		pw.Close()
		logf("controller: dial: %v", err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		viewer.Close()
		pw.Close()
		logf("controller: %s declined or unreachable (HTTP %d)", peerName, resp.StatusCode)
		return
	}

	conn := &readWriter{r: resp.Body, w: pw}
	// RunControllerSession closes the viewer when it returns.
	if err := rc.RunControllerSession(ctx, conn, viewer, logf); err != nil {
		logf("controller: session with %s ended: %v", peerName, err)
	}
	resp.Body.Close()
	pw.Close()
}
