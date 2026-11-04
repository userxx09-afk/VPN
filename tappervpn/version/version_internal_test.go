// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package version

import (
	"os/exec"
	"strings"
	"testing"

	"tappervpn.com/util/cibuild"
)

func TestIsValidLongWithTwoRepos(t *testing.T) {
	tests := []struct {
		long string
		want bool
	}{
		{"1.2.3-t01234abcde-g01234abcde", true},
		{"1.2.259-t01234abcde-g01234abcde", true}, // big patch version
		{"1.2.3-t01234abcde", false},              // missing repo
		{"1.2.3-g01234abcde", false},              // missing repo
		{"-t01234abcde-g01234abcde", false},
		{"1.2.3", false},
		{"1.2.3-t01234abcde-g", false},
		{"1.2.3-t01234abcde-gERRBUILDINFO", false},
	}
	for _, tt := range tests {
		if got := isValidLongWithTwoRepos(tt.long); got != tt.want {
			t.Errorf("IsValidLongWithTwoRepos(%q) = %v; want %v", tt.long, got, tt.want)
		}
	}
}

func TestTapperVPNToolchainRev(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	goRoot := strings.TrimSpace(string(out))
	isTsgo := strings.Contains(goRoot, "/.cache/tsgo/")
	if !cibuild.On() && !isTsgo {
		t.Skip("skipping; not in CI and not using the TapperVPN Go toolchain")
	}
	if !isTapperVPNGo {
		t.Skip("skipping; not built with tappervpn_go build tag")
	}
	rev := tappervpnToolchainRev()
	if rev == "" {
		t.Fatal("tappervpn.toolchain.rev is empty in build info; expected non-empty when using tsgo")
	}
	t.Logf("tappervpn.toolchain.rev = %s", rev)
}

func TestPrepExeNameForCmp(t *testing.T) {
	cases := []struct {
		exe  string
		want string
	}{
		{
			"tappervpn-ipn.exe",
			"tappervpn-ipn",
		},
		{
			"tappervpn-gui-amd64.exe",
			"tappervpn-gui",
		},
		{
			"tappervpn-gui-amd64",
			"tappervpn-gui",
		},
		{
			"tappervpn-ipn",
			"tappervpn-ipn",
		},
		{
			"tappervpn-iPn.ExE",
			"tappervpn-ipn",
		},
	}

	for _, c := range cases {
		got := prepExeNameForCmp(c.exe, "amd64")
		if got != c.want {
			t.Errorf("prepExeNameForCmp(%q) = %q; want %q", c.exe, got, c.want)
		}
	}
}
