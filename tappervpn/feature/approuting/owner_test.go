// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"tappervpn.com/types/ipproto"
)

type fakeOS struct {
	rows      []sockRow
	fetchErr  error
	fetches   int
	paths     map[uint32]string
	pathCalls int
	clock     time.Time
}

func (f *fakeOS) lookup() *ownerLookup {
	o := newOwnerLookup(
		func() ([]sockRow, error) {
			f.fetches++
			return f.rows, f.fetchErr
		},
		func(pid uint32) (string, error) {
			f.pathCalls++
			p, ok := f.paths[pid]
			if !ok {
				return "", errors.New("no such process")
			}
			return p, nil
		},
	)
	o.now = func() time.Time { return f.clock }
	return o
}

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func TestOwnerLookup(t *testing.T) {
	f := &fakeOS{
		clock: time.Unix(1000, 0),
		rows: []sockRow{
			{ipproto.TCP, ap("100.64.0.1:50000"), 10},
			{ipproto.UDP, ap("0.0.0.0:5353"), 20},
			{ipproto.UDP, ap("[::]:6000"), 30},
			{ipproto.TCP, ap("[fd7a:115c:a1e0::1]:50001"), 10},
			{ipproto.TCP, ap("100.64.0.1:50002"), 99}, // process gone
			{ipproto.TCP, ap("100.64.0.1:50003"), 0},  // idle PID
		},
		paths: map[uint32]string{
			10: `C:\app\chrome.exe`,
			20: `C:\app\mdns.exe`,
			30: `C:\app\dual.exe`,
		},
	}
	o := f.lookup()

	tests := []struct {
		name  string
		proto ipproto.Proto
		src   string
		want  string // "" means unidentified
	}{
		{"tcp exact", ipproto.TCP, "100.64.0.1:50000", `C:\app\chrome.exe`},
		{"tcp v6 exact", ipproto.TCP, "[fd7a:115c:a1e0::1]:50001", `C:\app\chrome.exe`},
		{"udp v4 wildcard", ipproto.UDP, "100.64.0.1:5353", `C:\app\mdns.exe`},
		{"udp v6 wildcard for v4 flow", ipproto.UDP, "100.64.0.1:6000", `C:\app\dual.exe`},
		{"udp v6 wildcard for v6 flow", ipproto.UDP, "[fd7a:115c:a1e0::1]:6000", `C:\app\dual.exe`},
		{"4in6 source unmapped", ipproto.TCP, "[::ffff:100.64.0.1]:50000", `C:\app\chrome.exe`},
		{"proto must match", ipproto.UDP, "100.64.0.1:50000", ""},
		{"tcp has no wildcard fallback here", ipproto.TCP, "100.64.0.1:5353", ""},
		{"unknown port", ipproto.TCP, "100.64.0.1:1", ""},
		{"process gone", ipproto.TCP, "100.64.0.1:50002", ""},
		{"idle pid", ipproto.TCP, "100.64.0.1:50003", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.clock = f.clock.Add(time.Second) // allow refreshes
			app := o.Lookup(tt.proto, ap(tt.src))
			got := ""
			if app != nil {
				got = app.Path
			}
			if got != tt.want {
				t.Errorf("Lookup(%v, %s) = %q, want %q", tt.proto, tt.src, got, tt.want)
			}
		})
	}
}

func TestOwnerLookupRefreshRateLimit(t *testing.T) {
	f := &fakeOS{clock: time.Unix(1000, 0), paths: map[uint32]string{1: `C:\a.exe`}}
	o := f.lookup()

	// First miss triggers a fetch.
	if app := o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")); app != nil {
		t.Fatalf("got %v, want nil", app)
	}
	if f.fetches != 1 {
		t.Fatalf("fetches = %d, want 1", f.fetches)
	}

	// The socket appears, but a lookup within minRefresh must not refetch.
	f.rows = []sockRow{{ipproto.TCP, ap("100.64.0.1:1000"), 1}}
	f.clock = f.clock.Add(o.minRefresh / 2)
	if app := o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")); app != nil {
		t.Fatalf("got %v within rate limit, want nil", app)
	}
	if f.fetches != 1 {
		t.Fatalf("fetches = %d, want still 1", f.fetches)
	}

	// After minRefresh, the miss refetches and finds it.
	f.clock = f.clock.Add(o.minRefresh)
	if app := o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")); app == nil || app.Path != `C:\a.exe` {
		t.Fatalf("got %v, want C:\\a.exe", app)
	}
	if f.fetches != 2 {
		t.Fatalf("fetches = %d, want 2", f.fetches)
	}

	// A hit never refetches.
	f.clock = f.clock.Add(time.Hour)
	o.Lookup(ipproto.TCP, ap("100.64.0.1:1000"))
	if f.fetches != 2 {
		t.Fatalf("fetches = %d after hit, want 2", f.fetches)
	}
}

func TestOwnerLookupFetchErrorKeepsSnapshot(t *testing.T) {
	f := &fakeOS{
		clock: time.Unix(1000, 0),
		rows:  []sockRow{{ipproto.TCP, ap("100.64.0.1:1000"), 1}},
		paths: map[uint32]string{1: `C:\a.exe`},
	}
	o := f.lookup()
	if o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")) == nil {
		t.Fatal("initial lookup failed")
	}
	f.fetchErr = errors.New("boom")
	f.clock = f.clock.Add(time.Second)
	o.Lookup(ipproto.TCP, ap("100.64.0.1:2")) // miss → failed refresh
	if o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")) == nil {
		t.Fatal("previous snapshot was discarded after a fetch error")
	}
}

func TestOwnerLookupPathCache(t *testing.T) {
	f := &fakeOS{
		clock: time.Unix(1000, 0),
		rows:  []sockRow{{ipproto.TCP, ap("100.64.0.1:1000"), 7}},
		paths: map[uint32]string{7: `C:\old.exe`},
	}
	o := f.lookup()
	o.Lookup(ipproto.TCP, ap("100.64.0.1:1000"))
	o.Lookup(ipproto.TCP, ap("100.64.0.1:1000"))
	if f.pathCalls != 1 {
		t.Fatalf("pathCalls = %d, want 1 (cached)", f.pathCalls)
	}

	// PID reused by another program: visible once the cache entry expires.
	f.paths[7] = `C:\new.exe`
	f.clock = f.clock.Add(o.pathTTL)
	if app := o.Lookup(ipproto.TCP, ap("100.64.0.1:1000")); app == nil || app.Path != `C:\new.exe` {
		t.Fatalf("after TTL got %v, want C:\\new.exe", app)
	}
}
