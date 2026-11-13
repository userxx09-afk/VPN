// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"net/http"

	"tappervpn.com/ipn/ipnlocal"
)

// handleConnect is the host (controlled) side of the connection. It runs inside
// the node's PeerAPI server, so the request is already authenticated to come
// from the calling peer over the encrypted mesh; h.Peer() is that peer.
//
// The flow enforces the core requirement — no control without acceptance:
//  1. If this node has no host agent, control is simply unavailable.
//  2. Otherwise the user is prompted (unless this peer was remembered), and a
//     denial or timeout returns 403 before any stream is established.
//  3. Only on acceptance is the response opened and the session run.
func handleConnect(h ipnlocal.PeerAPIHandler, w http.ResponseWriter, r *http.Request) {
	agent := getHostAgent()
	if agent == nil {
		http.Error(w, "remote control is not available on this device", http.StatusNotImplemented)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	peer := h.Peer()
	req := ConsentRequest{
		PeerName: peerDisplayName(peer),
		PeerID:   string(peer.StableID()),
	}

	if !isRemembered(req.PeerID) {
		ctx, cancel := context.WithTimeout(r.Context(), consentTimeout)
		decision := agent.RequestConsent(ctx, req)
		cancel()
		if !decision.Allow {
			h.Logf("remotecontrol: declined control request from %q", req.PeerName)
			http.Error(w, "connection declined", http.StatusForbidden)
			return
		}
		if decision.Remember {
			rememberPeer(req.PeerID)
		}
	}

	// Accept: upgrade to a raw bidirectional connection and run the session.
	// (A streaming HTTP body can't carry the duplex link; see upgrade.go.)
	conn, err := hijackUpgrade(w, r)
	if err != nil {
		h.Logf("remotecontrol: upgrade failed for %q: %v", req.PeerName, err)
		return
	}
	defer conn.Close()

	h.Logf("remotecontrol: accepted control session from %q", req.PeerName)
	if err := RunHostSession(r.Context(), conn, agent, req, h.Logf); err != nil {
		h.Logf("remotecontrol: host session with %q ended: %v", req.PeerName, err)
	}
}
