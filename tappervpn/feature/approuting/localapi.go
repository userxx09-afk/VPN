// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"encoding/json"
	"io"
	"net/http"

	"tappervpn.com/ipn/localapi"
	"tappervpn.com/util/httpm"
)

// serveLocalAPI is LocalAPI "app-routing", used by the TapperVPN app.
//
//	GET:  the per-app routing status (see [status])
//	POST: {"policy": <Policy> | null} sets this device's policy (null: every
//	      app follows the exit node), then returns the status
func serveLocalAPI(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	var e *extension
	if !h.LocalBackend().FindMatchingExtension(&e) {
		http.Error(w, "per-app routing isn't available on this device", http.StatusNotImplemented)
		return
	}
	switch r.Method {
	case httpm.GET:
		if !h.PermitRead {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
	case httpm.POST:
		if !h.PermitWrite {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		var body struct {
			Policy json.RawMessage `json:"policy"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		var doc []byte
		if len(body.Policy) > 0 && string(body.Policy) != "null" {
			doc = body.Policy
		}
		if err := e.setLocal(doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(e.status())
}
