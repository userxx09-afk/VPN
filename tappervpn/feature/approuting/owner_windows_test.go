// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"

	"tappervpn.com/types/ipproto"
)

func TestPortConversion(t *testing.T) {
	// Port 443 (0x01BB) in network byte order, read as a little-endian DWORD.
	if got := port(0xBB01); got != 443 {
		t.Errorf("port(0xBB01) = %d, want 443", got)
	}
	// 127.0.0.1 in network byte order, read as a little-endian DWORD.
	if got := addr4(0x0100007f); got != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("addr4 = %v, want 127.0.0.1", got)
	}
}

// TestOSOwnerLookupLive opens real sockets and checks that the live socket
// table attributes them to this test binary.
func TestOSOwnerLookupLive(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := newOSOwnerLookup()
	o.selfPID = 0 // the sockets under test are our own; report them by path

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	uc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0}) // wildcard, dual-stack
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()

	tests := []struct {
		name  string
		proto ipproto.Proto
		src   netip.AddrPort
	}{
		{"tcp client", ipproto.TCP, c.LocalAddr().(*net.TCPAddr).AddrPort()},
		{"udp wildcard", ipproto.UDP, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(uc.LocalAddr().(*net.UDPAddr).Port))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := o.Lookup(tt.proto, tt.src)
			if app == nil {
				t.Fatalf("Lookup(%v, %v) = nil, want %s", tt.proto, tt.src, self)
			}
			if !strings.EqualFold(NormalizeWindowsPath(app.Path), NormalizeWindowsPath(self)) {
				t.Errorf("Lookup(%v, %v) = %q, want %q", tt.proto, tt.src, app.Path, self)
			}
		})
	}
}

func TestProcessImagePathSystem(t *testing.T) {
	if p, err := processImagePath(4); err != nil || p != "System" {
		t.Errorf("processImagePath(4) = %q, %v; want System", p, err)
	}
	if _, err := processImagePath(0); err == nil {
		t.Error("processImagePath(0) succeeded, want error")
	}
}
