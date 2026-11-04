// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main // import "tappervpn.com/cmd/tappervpnd"

import (
	"os"
	"strings"
	"testing"

	"tappervpn.com/envknob"
	"tappervpn.com/ipn"
	"tappervpn.com/net/netmon"
	"tappervpn.com/tsd"
	"tappervpn.com/tstest/deptest"
	"tappervpn.com/types/logid"
	"tappervpn.com/util/must"
)

func TestNothing(t *testing.T) {
	// This test does nothing on purpose, so we can run
	// GODEBUG=memprofilerate=1 go test -v -run=Nothing -memprofile=prof.mem
	// without any errors about no matching tests.
}

func TestDeps(t *testing.T) {
	deptest.DepChecker{
		GOOS:   "darwin",
		GOARCH: "arm64",
		BadDeps: map[string]string{
			"testing":                        "do not use testing package in production code",
			"gvisor.dev/gvisor/pkg/hostarch": "will crash on non-4K page sizes; see https://github.com/tailscale/tailscale/issues/8658",
			"net/http/httptest":              "do not use httptest in production code",
			"net/http/internal/testcert":     "do not use httptest in production code",
		},
	}.Check(t)

	deptest.DepChecker{
		GOOS:   "linux",
		GOARCH: "arm64",
		BadDeps: map[string]string{
			"testing":                                        "do not use testing package in production code",
			"gvisor.dev/gvisor/pkg/hostarch":                 "will crash on non-4K page sizes; see https://github.com/tailscale/tailscale/issues/8658",
			"google.golang.org/protobuf/proto":               "unexpected",
			"github.com/prometheus/client_golang/prometheus": "use tappervpn.com/metrics in tappervpnd",
		},
	}.Check(t)
}

func TestStateStoreError(t *testing.T) {
	logID, err := logid.NewPrivateID()
	if err != nil {
		t.Fatal(err)
	}
	// Don't upload any logs from tests.
	envknob.SetNoLogsNoSupport()

	args.statedir = t.TempDir()
	args.tunname = "userspace-networking"

	t.Run("new-state", func(t *testing.T) {
		sys := tsd.NewSystem()
		sys.NetMon.Set(must.Get(netmon.New(sys.Bus.Get(), t.Logf)))
		lb, err := getLocalBackend(t.Context(), t.Logf, logID.Public(), sys)
		if err != nil {
			t.Fatal(err)
		}
		defer lb.Shutdown()
		if lb.HealthTracker().IsUnhealthy(ipn.StateStoreHealth) {
			t.Errorf("StateStoreHealth is unhealthy on fresh LocalBackend:\n%s", strings.Join(lb.HealthTracker().Strings(), "\n"))
		}
	})
	t.Run("corrupt-state", func(t *testing.T) {
		sys := tsd.NewSystem()
		sys.NetMon.Set(must.Get(netmon.New(sys.Bus.Get(), t.Logf)))
		// Populate the state file with something that will fail to parse to
		// trigger an error from store.New.
		if err := os.WriteFile(statePathOrDefault(), []byte("bad json"), 0644); err != nil {
			t.Fatal(err)
		}
		lb, err := getLocalBackend(t.Context(), t.Logf, logID.Public(), sys)
		if err != nil {
			t.Fatal(err)
		}
		defer lb.Shutdown()
		if !lb.HealthTracker().IsUnhealthy(ipn.StateStoreHealth) {
			t.Errorf("StateStoreHealth is healthy when state file is corrupt")
		}
	})
}

func TestIsPortableStore(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "kube_store",
			path: "kube:my-secret",
			want: true,
		},
		{
			name: "aws_arn_store",
			path: "arn:aws:ssm:us-east-1:123456789012:parameter/tappervpn/state",
			want: true,
		},
		{
			name: "tpm_store",
			path: "tpmseal:/var/lib/tappervpn/tappervpnd.state",
			want: false,
		},
		{
			name: "local_file_store",
			path: "/var/lib/tappervpn/tappervpnd.state",
			want: false,
		},
		{
			name: "empty_path",
			path: "",
			want: false,
		},
		{
			name: "mem_store",
			path: "mem:",
			want: true,
		},
		{
			name: "windows_file_store",
			path: `C:\ProgramData\TapperVPN\server-state.conf`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPortableStore(tt.path)
			if got != tt.want {
				t.Errorf("isPortableStore(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
