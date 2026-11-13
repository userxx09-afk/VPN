// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"encoding/json"
	"net/http"

	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/localapi"
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

// serveConnect starts an in-process controller session to the target named by
// the "id" query parameter (its StableNodeID) and blocks for the session's
// lifetime. It is used by controllers whose viewer runs in this process; on
// Windows the separate helper brokers its own session instead and does not call
// this. POST /localapi/v0/remotecontrol/connect?id=<stableID>.
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
	err := Connect(r.Context(), h.LocalBackend(), id, ext.logf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
