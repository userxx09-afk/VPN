// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/tailcfg"
)

type fakePeerAPI struct {
	peer, self tailcfg.NodeView
}

func (f fakePeerAPI) Peer() tailcfg.NodeView               { return f.peer }
func (f fakePeerAPI) PeerCaps() tailcfg.PeerCapMap         { return nil }
func (f fakePeerAPI) CanDebug() bool                       { return false }
func (f fakePeerAPI) Self() tailcfg.NodeView               { return f.self }
func (f fakePeerAPI) LocalBackend() *ipnlocal.LocalBackend { return nil }
func (f fakePeerAPI) IsSelfUntagged() bool                 { return false }
func (f fakePeerAPI) RemoteAddr() netip.AddrPort           { return netip.AddrPort{} }
func (f fakePeerAPI) Logf(format string, a ...any)         {}

type fakeRestarter struct {
	device bool
	done   chan RestartKind
}

func (r *fakeRestarter) CanRestart(k RestartKind) bool {
	return k == RestartVPN || (k == RestartDevice && r.device)
}
func (r *fakeRestarter) Restart(k RestartKind, from string) error { r.done <- k; return nil }

func TestHandleRestart(t *testing.T) {
	allowed := (&tailcfg.Node{Name: "pc.", ComputedName: "pc", Tags: []string{"tag:workstation", RestarterTag}}).View()
	plain := (&tailcfg.Node{Name: "phone.", ComputedName: "phone", Tags: []string{"tag:device"}}).View()
	self := (&tailcfg.Node{Name: "tv.", ComputedName: "tv"}).View()

	fr := &fakeRestarter{done: make(chan RestartKind, 1)}
	RegisterRestarter(fr)
	defer RegisterRestarter(nil)

	for _, tc := range []struct {
		name     string
		method   string
		peer     tailcfg.NodeView
		what     string
		wantCode int
		wantKind RestartKind // "" means no restart happens
	}{
		{"allowed, vpn", "POST", allowed, "vpn", 200, RestartVPN},
		{"allowed, default is vpn", "POST", allowed, "", 200, RestartVPN},
		{"not allowed on the dashboard", "POST", plain, "vpn", 403, ""},
		{"untagged peer", "POST", (&tailcfg.Node{}).View(), "vpn", 403, ""},
		{"device reboot not supported here", "POST", allowed, "device", 501, ""},
		{"bad kind", "POST", allowed, "everything", 400, ""},
		{"GET refused", "GET", allowed, "vpn", 405, ""},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, "/remotecontrol/v0/restart?what="+tc.what, nil)
		handleRestart(fakePeerAPI{peer: tc.peer, self: self}, w, r)
		if w.Code != tc.wantCode {
			t.Errorf("%s: code %d, want %d (%s)", tc.name, w.Code, tc.wantCode, strings.TrimSpace(w.Body.String()))
		}
		select {
		case k := <-fr.done:
			if k != tc.wantKind {
				t.Errorf("%s: restarted %q, want %q", tc.name, k, tc.wantKind)
			}
		case <-time.After(restartDelay + time.Second):
			if tc.wantKind != "" {
				t.Errorf("%s: no restart, want %q", tc.name, tc.wantKind)
			}
		}
	}

	fr.device = true
	w := httptest.NewRecorder()
	handleRestart(fakePeerAPI{peer: allowed, self: self}, w, httptest.NewRequest("POST", "/remotecontrol/v0/restart?what=device", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "reboot") {
		t.Errorf("device reboot: %d %s", w.Code, w.Body.String())
	}
	if k := <-fr.done; k != RestartDevice {
		t.Errorf("restarted %q, want device", k)
	}
}

func TestOSCanReboot(t *testing.T) {
	for os, want := range map[string]bool{"windows": true, "linux": true, "android": false, "iOS": false, "macOS": false, "": false} {
		if got := osCanReboot(os); got != want {
			t.Errorf("osCanReboot(%q) = %v, want %v", os, got, want)
		}
	}
}
