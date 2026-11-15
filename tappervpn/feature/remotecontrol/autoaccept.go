// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"encoding/json"
	"net/http"

	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/localapi"
	"tappervpn.com/tailcfg"
)

// Auto-accept lets a trusted device take control without anyone answering the
// Allow prompt. It takes two things, so neither side alone can turn it on:
//
//  1. On the controlled device, its own user switches "Auto accept" on
//     (stored in this node's state, so it survives restarts).
//  2. On the Tapper Dashboard, the controlling device is marked as a trusted
//     controller, which gives it TrustedControllerTag. Tags come from the
//     control server in the signed network map; a device can't fake one.
//
// Turning either off brings the prompt back at once. The controlled device
// still shows that it is being controlled for the whole session.
const TrustedControllerTag = "tag:remote-admin"

const autoAcceptStateKey ipn.StateKey = "_remotecontrol-autoaccept"

// AutoAccept reports whether this device's user has switched auto-accept on.
func (e *Extension) AutoAccept() bool {
	st, ok := e.store()
	if !ok {
		return false
	}
	b, err := st.ReadState(autoAcceptStateKey)
	return err == nil && string(b) == "on"
}

// SetAutoAccept switches auto-accept on or off for this device.
func (e *Extension) SetAutoAccept(on bool) error {
	st, ok := e.store()
	if !ok {
		return errNoStore
	}
	v := "off"
	if on {
		v = "on"
	}
	return ipn.WriteState(st, autoAcceptStateKey, []byte(v))
}

func (e *Extension) store() (ipn.StateStore, bool) {
	if e.sb == nil || e.sb.Sys() == nil {
		return nil, false
	}
	return e.sb.Sys().StateStore.GetOK()
}

type storeError string

func (s storeError) Error() string { return string(s) }

const errNoStore = storeError("this device has no state store to keep the setting in")

// isTrustedController reports whether peer is marked on the dashboard as a
// device allowed to take control without a prompt.
func isTrustedController(peer tailcfg.NodeView) bool {
	if !peer.Valid() {
		return false
	}
	for _, t := range peer.Tags().All() {
		if t == TrustedControllerTag {
			return true
		}
	}
	return false
}

// shouldAutoAccept is the whole rule: this device opted in AND the caller is a
// dashboard-marked trusted controller.
func shouldAutoAccept(optedIn bool, peer tailcfg.NodeView) bool {
	return optedIn && isTrustedController(peer)
}

// AutoAcceptStatus is the GET/POST body of the autoaccept endpoint.
type AutoAcceptStatus struct {
	On bool `json:"on"`
	// TrustedTag is the dashboard tag a controller needs; shown in the UI.
	TrustedTag string `json:"trustedTag"`
}

// serveAutoAccept reads (GET) or sets (POST {"on":true|false}) this device's
// auto-accept switch. GET/POST /localapi/v0/remotecontrol/autoaccept.
func serveAutoAccept(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	ext, ok := ipnlocal.GetExt[*Extension](h.LocalBackend())
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.PermitRead {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
	case http.MethodPost:
		if !h.PermitWrite {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		var body AutoAcceptStatus
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad JSON body", http.StatusBadRequest)
			return
		}
		if err := ext.SetAutoAccept(body.On); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ext.logf("auto accept switched %s on this device", map[bool]string{true: "on", false: "off"}[body.On])
	default:
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AutoAcceptStatus{On: ext.AutoAccept(), TrustedTag: TrustedControllerTag})
}
