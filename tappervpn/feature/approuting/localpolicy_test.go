// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"testing"

	"tappervpn.com/ipn/store/mem"
)

func TestLocalPolicy(t *testing.T) {
	st := new(mem.Store)
	e := &extension{logf: t.Logf, store: st, router: NewRouter(RouterOptions{Logf: t.Logf})}

	if err := e.setLocal([]byte(`{"version":1,"default":"direct","rules":[{"action":"gateway","apps":[{"windows":{"exe":"chrome.exe"}}]}]}`)); err != nil {
		t.Fatal(err)
	}
	s := e.status()
	if s.Source != "app" || s.Policy == nil || s.Error != "" {
		t.Fatalf("after set: %+v", s)
	}
	if b, err := st.ReadState(localStateKey); err != nil || len(b) == 0 {
		t.Fatalf("not saved: %v", err)
	}

	// An invalid policy is refused and the previous one stays.
	if err := e.setLocal([]byte(`{"version":1,"default":"sideways"}`)); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if s := e.status(); s.Source != "app" {
		t.Fatalf("invalid policy replaced the good one: %+v", s)
	}

	// The control server's policy wins, and the app can see that.
	e.mu.Lock()
	e.controlDoc = []string{`{"version":1,"default":"gateway"}`}
	e.applyLocked()
	e.mu.Unlock()
	if s := e.status(); s.Source != "control" || !s.ControlSet {
		t.Fatalf("control policy: %+v", s)
	}
	e.mu.Lock()
	e.controlDoc = nil
	e.applyLocked()
	e.mu.Unlock()

	// Clearing it: every app follows the exit node again.
	if err := e.setLocal(nil); err != nil {
		t.Fatal(err)
	}
	if s := e.status(); s.Source != "" || s.Policy != nil {
		t.Fatalf("after clear: %+v", s)
	}
	if b, _ := st.ReadState(localStateKey); len(b) != 0 {
		t.Fatal("still saved after clear")
	}
}
