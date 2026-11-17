// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package ipnlocal

import (
	"slices"
	"testing"

	"tappervpn.com/ipn"
	"tappervpn.com/tailcfg"
)

func TestParseExitOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps []string
		want []string
	}{
		{"none", []string{"cache-network-maps"}, nil},
		{"default", []string{"cache-network-maps", "exit-order:tapper, CH-Workstation ,tapper-standby"},
			[]string{"tapper", "ch-workstation", "tapper-standby"}},
		{"device wins over default", []string{"exit-order:tapper,ch-workstation", "exit-order-device:ch-workstation,tapper"},
			[]string{"ch-workstation", "tapper"}},
		{"device wins regardless of order", []string{"exit-order-device:tapper-standby", "exit-order:tapper"},
			[]string{"tapper-standby"}},
		{"empty device list falls back", []string{"exit-order-device:", "exit-order:tapper"}, []string{"tapper"}},
	} {
		if got := parseExitOrder(tc.caps); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPickOrderedExit(t *testing.T) {
	cands := []exitCandidate{
		{id: "tapper", known: true, exit: true},
		{id: "chws", known: true, exit: true},
		{id: "standby", known: true, exit: true},
	}
	down := func(ids ...tailcfg.StableNodeID) map[tailcfg.StableNodeID]*exitHealth {
		m := map[tailcfg.StableNodeID]*exitHealth{}
		for _, id := range ids {
			m[id] = &exitHealth{down: true}
		}
		return m
	}
	for _, tc := range []struct {
		name   string
		cands  []exitCandidate
		health map[tailcfg.StableNodeID]*exitHealth
		want   tailcfg.StableNodeID
	}{
		{"all up: first", cands, nil, "tapper"},
		{"tapper down: ch-workstation", cands, down("tapper"), "chws"},
		{"both down: standby", cands, down("tapper", "chws"), "standby"},
		{"all down: none", cands, down("tapper", "chws", "standby"), ""},
		{"not offering exit is skipped", []exitCandidate{{id: "tapper", known: true}, {id: "chws", known: true, exit: true}}, nil, "chws"},
		{"unknown is skipped", []exitCandidate{{name: "gone"}, {id: "chws", known: true, exit: true}}, nil, "chws"},
	} {
		if got := pickOrderedExit(tc.cands, tc.health); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExitOrderManaged(t *testing.T) {
	cands := []exitCandidate{{id: "tapper", known: true, exit: true}, {id: "chws", known: true, exit: true}}
	for _, tc := range []struct {
		name  string
		prefs ipn.Prefs
		want  bool
	}{
		{"using a listed exit", ipn.Prefs{ExitNodeID: "chws"}, true},
		{"no exit node", ipn.Prefs{}, false},
		{"some other exit node", ipn.Prefs{ExitNodeID: "elsewhere"}, false},
		{"automatic exit node is left alone", ipn.Prefs{ExitNodeID: "tapper", AutoExitNode: ipn.AnyExitNode}, false},
	} {
		p := tc.prefs
		if got := exitOrderManagedLocked(&p, cands); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
