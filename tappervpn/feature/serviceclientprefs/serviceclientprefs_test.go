// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package serviceclientprefs

import (
	"testing"
	"time"

	"tappervpn.com/client/tappervpn/apitype"
	"tappervpn.com/ipn"
	"tappervpn.com/tsd"
	"tappervpn.com/tstest"
	"tappervpn.com/tstime"
	"tappervpn.com/types/logger"
)

// fakeBackend is a minimal [ipnext.SafeBackend] for tests. Only Clock and TapperVPNVarRoot are
// used by the extension; Sys is never called.
type fakeBackend struct {
	clock   tstime.Clock
	varRoot string
}

func (f *fakeBackend) Sys() *tsd.System         { return nil }
func (f *fakeBackend) Clock() tstime.Clock      { return f.clock }
func (f *fakeBackend) TapperVPNVarRoot() string { return f.varRoot }

// newTestExtension returns an extension backed by a temp var root and a fixed test clock, with its
// current profile set to pid.
func newTestExtension(t *testing.T, pid ipn.ProfileID) *extension {
	t.Helper()
	e := &extension{
		logf: logger.Discard,
		sb: &fakeBackend{
			clock:   tstest.NewClock(tstest.ClockOpts{Start: time.Now()}),
			varRoot: t.TempDir(),
		},
	}
	e.onChangeProfile((&ipn.LoginProfile{ID: pid}).View(), ipn.PrefsView{}, false)
	return e
}

func TestNoVarRootUsesMemory(t *testing.T) {
	e := &extension{
		logf: logger.Discard,
		sb: &fakeBackend{
			clock:   tstest.NewClock(tstest.ClockOpts{Start: time.Now()}),
			varRoot: "", // force the in-memory fallback
		},
	}
	e.onChangeProfile((&ipn.LoginProfile{ID: "pid"}).View(), ipn.PrefsView{}, false)

	if _, err := e.setServiceClientPref(apitype.ServiceClientPrefRequest{Key: "ssh:22", Client: "terminal"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.serviceClientPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if got["ssh:22"].Client != "terminal" {
		t.Errorf("in-memory store: got %v, want Client=terminal", got["ssh:22"])
	}
}
