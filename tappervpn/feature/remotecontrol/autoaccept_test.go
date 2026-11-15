// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"testing"

	"tappervpn.com/tailcfg"
)

func TestShouldAutoAccept(t *testing.T) {
	trusted := (&tailcfg.Node{Tags: []string{"tag:workstation", TrustedControllerTag}}).View()
	plain := (&tailcfg.Node{Tags: []string{"tag:device"}}).View()
	untagged := (&tailcfg.Node{}).View()
	for _, tc := range []struct {
		name    string
		optedIn bool
		peer    tailcfg.NodeView
		want    bool
	}{
		{"opted in, trusted controller", true, trusted, true},
		{"opted in, ordinary device", true, plain, false},
		{"opted in, untagged device", true, untagged, false},
		{"not opted in, trusted controller", false, trusted, false},
		{"opted in, invalid peer", true, tailcfg.NodeView{}, false},
	} {
		if got := shouldAutoAccept(tc.optedIn, tc.peer); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
