// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build tappervpn_go

package tappervpnroot

import (
	"os"
	"strings"
	"testing"
)

func TestToolchainMatches(t *testing.T) {
	tsRev, ok := tappervpnToolchainRev()
	if !ok {
		t.Fatal("failed to read build info")
	}
	want := strings.TrimSpace(GoToolchainRev)
	if tsRev != want {
		if os.Getenv("TS_PERMIT_TOOLCHAIN_MISMATCH") == "1" {
			t.Logf("tappervpn.toolchain.rev = %q, want %q; but ignoring due to TS_PERMIT_TOOLCHAIN_MISMATCH=1", tsRev, want)
			return
		}
		t.Errorf("tappervpn.toolchain.rev = %q, want %q; permit with TS_PERMIT_TOOLCHAIN_MISMATCH=1", tsRev, want)
	}
}
