// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"net/http"
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

	setStatus(viewer, "Asking "+peerName+" to accept… (someone there has a minute to answer)", false)

	// Upgrade to a raw bidirectional connection; the daemon relays it to the
	// peer (whose user must accept first) and splices the two streams.
	conn, err := dialUpgrade(ctx, dialURL+"?id="+url.QueryEscape(peerID))
	if err != nil {
		logf("controller: %s declined or unreachable: %v", peerName, err)
		setStatus(viewer, dialFailureMessage(peerName, err), true)
		viewer.Close() // keeps the window open showing the message
		return
	}
	defer conn.Close()

	// RunControllerSession closes the viewer when it returns once the session
	// has started; a failure before that (no Accept) must still be shown.
	if err := rc.RunControllerSession(ctx, conn, viewer, logf); err != nil {
		logf("controller: session with %s ended: %v", peerName, err)
		if ctx.Err() == nil {
			setStatus(viewer, "The session with "+peerName+" ended: "+err.Error(), true)
		}
	}
	viewer.Close()
}

func setStatus(v rc.Viewer, msg string, final bool) {
	if sv, ok := v.(rc.StatusViewer); ok {
		sv.Status(msg, final)
	}
}

// dialFailureMessage turns a failed dial into words for the viewer window.
func dialFailureMessage(peerName string, err error) string {
	var se *statusError
	if errors.As(err, &se) {
		switch se.code {
		case http.StatusForbidden:
			return peerName + " declined, or nobody answered in time."
		case http.StatusNotImplemented:
			return peerName + " can't be controlled yet. Make sure TapperVPN there is up to date and running" +
				" (on a PC, the TapperVPN Remote Helper must be running; on Fire TV or Android, open the app once)."
		case http.StatusNotFound:
			return peerName + " isn't on your network right now."
		}
		if se.msg != "" {
			return "Couldn't connect to " + peerName + ": " + se.msg
		}
	}
	return "Couldn't connect to " + peerName + ": " + err.Error()
}
