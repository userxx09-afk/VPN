// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/localapi"
	"tappervpn.com/tailcfg"
)

// serveTargets lists the peers that a controller on this node may ask to
// control. GET /localapi/v0/remotecontrol/targets.
func serveTargets(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	if !h.PermitRead {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	ext, ok := ipnlocal.GetExt[*Extension](h.LocalBackend())
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ext.Targets())
}

// serveConnect starts a controller session to the target named by the "id"
// query parameter (its StableNodeID). If this process has an in-process viewer
// (e.g. Android), it runs the session here and blocks for its lifetime. If
// instead a user-session helper is attached (Windows), it tells the helper to
// open the session with its own viewer window and returns immediately.
// POST /localapi/v0/remotecontrol/connect?id=<stableID>.
func serveConnect(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	if !h.PermitWrite {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	ext, ok := ipnlocal.GetExt[*Extension](h.LocalBackend())
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}

	if ControllerCapable() {
		// In-process viewer (Android): run here for the session's lifetime.
		if err := Connect(r.Context(), h.LocalBackend(), id, ext.logf); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if helper := attachedHelper(); helper != nil {
		// Windows: hand off to the helper's viewer window. Include a friendly
		// name if the peer is known.
		name := id
		if peer, ok := ext.nodeBackend().PeerByStableID(tailcfg.StableNodeID(id)); ok {
			name = peerDisplayName(peer)
		}
		if err := helper.StartControlling(id, name); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Error(w, "no controller UI available on this device", http.StatusNotImplemented)
}

// serveDial is the controller relay used by the user-session helper. The helper
// POSTs here with the target's StableNodeID; the daemon dials that peer's
// PeerAPI connect endpoint and, on acceptance, splices the two streams so the
// helper runs the controller session directly against the peer. The remote
// consent prompt happens before the peer responds, so a decline surfaces as a
// non-200 here. POST /localapi/v0/remotecontrol/dial?id=<stableID>.
func serveDial(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	if !h.PermitWrite {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	b := h.LocalBackend()
	ext, ok := ipnlocal.GetExt[*Extension](b)
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}
	nb := ext.nodeBackend()
	peer, ok := nb.PeerByStableID(tailcfg.StableNodeID(id))
	if !ok {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	base := nb.PeerAPIBase(peer)
	if base == "" {
		http.Error(w, "peer not reachable over PeerAPI", http.StatusBadGateway)
		return
	}

	// Dial the peer first (this blocks during the remote user's consent), so a
	// decline is reported to the helper as a normal HTTP error before we upgrade
	// our own connection to the helper.
	client := &http.Client{Transport: b.Dialer().PeerAPITransport()}
	peerConn, err := dialUpgradeHTTP(r.Context(), client, base+"/remotecontrol/v0/connect")
	if err != nil {
		var se *httpStatusError
		if errors.As(err, &se) {
			http.Error(w, "peer declined", se.code)
		} else {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer peerConn.Close()

	// Accepted: upgrade our link to the helper and splice helper <-> peer raw.
	helperConn, err := hijackUpgrade(w, r)
	if err != nil {
		ext.logf("remotecontrol: dial upgrade to helper failed: %v", err)
		return
	}
	defer helperConn.Close()

	errc := make(chan error, 2)
	go func() { _, err := io.Copy(peerConn, helperConn); errc <- err }() // helper -> peer
	go func() { _, err := io.Copy(helperConn, peerConn); errc <- err }() // peer -> helper
	<-errc
}

// serveAgent attaches a user-session helper to the daemon for the duration of
// the request. Over this full-duplex stream the daemon proxies host-side work
// (consent, capture, input) to the helper; see agent.go. The helper is a local,
// trusted process, so this requires write permission like the other endpoints.
// POST /localapi/v0/remotecontrol/agent.
func serveAgent(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	if !h.PermitWrite {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	ext, ok := ipnlocal.GetExt[*Extension](h.LocalBackend())
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}
	conn, err := hijackUpgrade(w, r)
	if err != nil {
		ext.logf("remotecontrol: agent upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	if err := ServeAgentLink(conn, ext.logf); err != nil {
		ext.logf("remotecontrol: helper link ended: %v", err)
	}
}
