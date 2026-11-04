// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"tappervpn.com/net/packet"
	"tappervpn.com/net/tsaddr"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/ipproto"
	"tappervpn.com/wgengine/netstack"
)

func TestParseGateway(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"gateway rule", `{"version":1,"rules":[{"action":"gateway","gateway":"firetv","apps":[{"windows":{"exe":"a.exe"}}]}]}`, ""},
		{"gateway on direct rule", `{"version":1,"rules":[{"action":"direct","gateway":"firetv","apps":[{"windows":{"exe":"a.exe"}}]}]}`, `only gateway rules can name one`},
		{"gateway with space", `{"version":1,"rules":[{"action":"gateway","gateway":"fire tv","apps":[{"windows":{"exe":"a.exe"}}]}]}`, `invalid gateway name`},
		{"gatewayUnavailable valid", `{"version":1,"gatewayUnavailable":"gateway"}`, ""},
		{"gatewayUnavailable invalid", `{"version":1,"gatewayUnavailable":"nope"}`, `invalid gatewayUnavailable action`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestDecideGateway(t *testing.T) {
	p, err := Parse([]byte(`{"version":1,"default":"direct","rules":[
		{"name":"tv","action":"gateway","gateway":"FireTV","apps":[{"windows":{"exe":"a.exe"}}]},
		{"name":"exit","action":"gateway","apps":[{"windows":{"exe":"b.exe"}}]},
		{"name":"tv-again","action":"gateway","gateway":"firetv","apps":[{"windows":{"exe":"c.exe"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Compile(CompileOptions{GOOS: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Decide(&App{Path: `C:\a.exe`}); got != (Decision{ActionGateway, "FireTV", "tv"}) {
		t.Errorf("a.exe: %+v", got)
	}
	if got := m.Decide(&App{Path: `C:\b.exe`}); got != (Decision{ActionGateway, "", "exit"}) {
		t.Errorf("b.exe: %+v", got)
	}
	if got := m.GatewayUnavailable(); got != ActionBlock {
		t.Errorf("GatewayUnavailable default = %q, want block", got)
	}
	if got := m.Gateways(); len(got) != 1 || !strings.EqualFold(got[0], "firetv") {
		t.Errorf("Gateways() = %q, want one entry (case-insensitive dedupe)", got)
	}
}

func mustVia(t *testing.T, site uint32, pfx string) netip.Prefix {
	t.Helper()
	p, err := tsaddr.MapVia(site, netip.MustParsePrefix(pfx))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func peer(name, host string, online *bool, routes ...netip.Prefix) tailcfg.NodeView {
	return (&tailcfg.Node{
		Name:          name + ".meshwall.internal.",
		ComputedName:  name,
		Hostinfo:      (&tailcfg.Hostinfo{Hostname: host}).View(),
		PrimaryRoutes: routes,
		Online:        online,
	}).View()
}

func TestGatewaySites(t *testing.T) {
	yes, no := true, false
	peers := []tailcfg.NodeView{
		peer("shanes-fire-tv-cube", "SHANE's Fire TV Cube", &yes, mustVia(t, 7, "0.0.0.0/0")),
		peer("laptop", "laptop", nil, mustVia(t, 9, "0.0.0.0/0"), netip.MustParsePrefix("10.0.0.0/8")),
		peer("offline-gw", "offline-gw", &no, mustVia(t, 3, "0.0.0.0/0")),
		peer("subnet-only", "subnet-only", &yes, mustVia(t, 4, "192.168.0.0/16")), // not a full gateway
		peer("plain", "plain", &yes, netip.MustParsePrefix("0.0.0.0/0")),          // exit node, no 4via6
		peer("dup-a", "dup", &yes, mustVia(t, 11, "0.0.0.0/0")),
		peer("dup-b", "dup", &yes, mustVia(t, 12, "0.0.0.0/0")),
	}
	got := gatewaySites(peers)
	want := map[string]uint32{
		"shanes-fire-tv-cube":  7,
		"shane's fire tv cube": 7,
		"laptop":               9,
		"dup-a":                11,
		"dup-b":                12,
		// "dup" (the shared hostname) is ambiguous and left out.
	}
	if len(got) != len(want) {
		t.Errorf("gatewaySites = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("gatewaySites[%q] = %d, want %d", k, got[k], v)
		}
	}
}

func TestGatewaySiteOf(t *testing.T) {
	if s, ok := gatewaySiteOf(mustVia(t, 0x01020304, "0.0.0.0/0")); !ok || s != 0x01020304 {
		t.Errorf("site = %#x, %v", s, ok)
	}
	for _, p := range []netip.Prefix{
		mustVia(t, 5, "10.0.0.0/8"),
		netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
		netip.MustParsePrefix("0.0.0.0/0"),
	} {
		if _, ok := gatewaySiteOf(p); ok {
			t.Errorf("gatewaySiteOf(%v) = ok, want not a gateway route", p)
		}
	}
}

const gatewayPolicy = `{
	"version": 1,
	"default": "direct",
	"rules": [
		{"name": "via-tv", "action": "gateway", "gateway": "firetv", "apps": [{"windows": {"exe": "chrome.exe"}}]},
		{"name": "via-gone", "action": "gateway", "gateway": "gone", "apps": [{"windows": {"exe": "notepad.exe"}}]}
	]
}`

func newGatewayFixture(t *testing.T) *routerFixture {
	t.Helper()
	f := newRouterFixture(t)
	p, err := Parse([]byte(gatewayPolicy))
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Compile(CompileOptions{GOOS: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	f.r.SetMatcher(m)
	f.r.SetGateways(map[string]uint32{"FireTV": 7})
	f.r.dialViaTCP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		return nil, context.Canceled // replaced in tests that dial
	}
	return f
}

func TestClassifyGateway(t *testing.T) {
	tests := []struct {
		name string
		src  string // chrome is pid 1 on :40000, notepad pid 2 on :40001
		dst  string
		want netstack.LocalPacketVerdict
	}{
		{"named gateway online", "100.64.0.1:40000", "1.1.1.1:443", intercept},
		{"named gateway unknown blocks", "100.64.0.1:40001", "1.1.1.1:443", drop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newGatewayFixture(t)
			if got := f.r.Classify(rawPacket(t, ipproto.TCP, tt.src, tt.dst, packet.TCPSyn)); got != tt.want {
				t.Errorf("Classify = %v, want %v", got, tt.want)
			}
		})
	}

	// Gateway goes away: new flows get gatewayUnavailable, existing ones keep
	// their decision.
	f := newGatewayFixture(t)
	if got := f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40000", "1.1.1.1:443", packet.TCPSyn)); got != intercept {
		t.Fatalf("before: %v", got)
	}
	f.r.SetGateways(nil)
	if got := f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40000", "1.1.1.1:443", packet.TCPAck)); got != intercept {
		t.Errorf("existing flow after gateway left: %v, want intercept", got)
	}
	f.os.rows = append(f.os.rows, sockRow{ipproto.TCP, ap("100.64.0.1:40005"), 1})
	f.clock = f.clock.Add(time.Second)
	f.os.clock = f.os.clock.Add(time.Second) // let the socket table refresh
	if got := f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40005", "1.1.1.1:443", packet.TCPSyn)); got != drop {
		t.Errorf("new flow after gateway left: %v, want drop", got)
	}
}

func TestClassifyGatewayUnavailableOverride(t *testing.T) {
	f := newRouterFixture(t)
	p, err := Parse([]byte(`{"version":1,"default":"direct","gatewayUnavailable":"gateway","rules":[
		{"action":"gateway","gateway":"gone","apps":[{"windows":{"exe":"chrome.exe"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := p.Compile(CompileOptions{GOOS: "windows"})
	f.r.SetMatcher(m)
	f.r.dialViaTCP = func(context.Context, netip.AddrPort) (net.Conn, error) { return nil, context.Canceled }
	if got := f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40000", "1.1.1.1:443", packet.TCPSyn)); got != pass {
		t.Errorf("Classify = %v, want passthrough to the exit node", got)
	}
}

func TestTCPHandlerViaGateway(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				c.Write([]byte("gw:" + line))
			}()
		}
	}()

	f := newGatewayFixture(t)
	var dialed netip.AddrPort
	f.r.dialViaTCP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		dialed = dst
		return net.Dial("tcp", ln.Addr().String())
	}
	f.r.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("gateway flow used the direct dialer")
		return nil, context.Canceled
	}

	f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40000", "203.0.113.5:443", packet.TCPSyn))
	h, ok := f.r.TCPHandlerForFlow(ap("100.64.0.1:40000"), ap("203.0.113.5:443"))
	if h == nil || !ok {
		t.Fatalf("got (%v, %v), want handler", h != nil, ok)
	}
	wantVia := netip.AddrPortFrom(mustVia(t, 7, "203.0.113.5/32").Addr(), 443)
	if dialed != wantVia {
		t.Errorf("dialed %v, want %v", dialed, wantVia)
	}
	if tsaddr.UnmapVia(dialed.Addr()) != netip.MustParseAddr("203.0.113.5") {
		t.Errorf("via address doesn't unmap to the destination: %v", dialed)
	}

	appSide, nsSide := net.Pipe()
	go h(nsSide)
	appSide.SetDeadline(time.Now().Add(5 * time.Second))
	appSide.Write([]byte("hello\n"))
	got, err := bufio.NewReader(appSide).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got != "gw:hello\n" {
		t.Errorf("relayed %q", got)
	}
	appSide.Close()
}

func TestUDPHandlerViaGateway(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			echo.WriteTo(append([]byte("gw:"), buf[:n]...), from)
		}
	}()

	f := newGatewayFixture(t)
	f.os.rows = append(f.os.rows, sockRow{ipproto.UDP, ap("0.0.0.0:6000"), 1}) // chrome
	var dialed netip.AddrPort
	f.r.dialViaUDP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		dialed = dst
		return net.DialUDP("udp4", nil, echo.LocalAddr().(*net.UDPAddr))
	}

	src, dst := ap("100.64.0.1:6000"), ap("203.0.113.9:3478")
	if got := f.r.Classify(rawPacket(t, ipproto.UDP, src.String(), dst.String(), 0)); got != intercept {
		t.Fatalf("Classify = %v, want intercept", got)
	}
	h, ok := f.r.UDPHandlerForFlow(src, dst)
	if h == nil || !ok {
		t.Fatal("via UDP flow not intercepted")
	}
	if want := netip.AddrPortFrom(mustVia(t, 7, "203.0.113.9/32").Addr(), 3478); dialed != want {
		t.Errorf("dialed %v, want %v", dialed, want)
	}

	appSide, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer appSide.Close()
	nsConn, err := net.DialUDP("udp4", nil, appSide.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	go h(nsConn)
	appSide.SetDeadline(time.Now().Add(5 * time.Second))
	appSide.WriteTo([]byte("ping"), nsConn.LocalAddr())
	buf := make([]byte, 100)
	n, _, err := appSide.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "gw:ping" {
		t.Errorf("relayed %q, want %q", got, "gw:ping")
	}
}

func TestClassifyGatewayIPv6Unavailable(t *testing.T) {
	f := newGatewayFixture(t)
	// IPv6 source/destination: 4via6 can't carry it, so gatewayUnavailable.
	k := flowKey{ipproto.TCP, ap("[fd7a:115c:a1e0::1]:40000"), ap("[2001:db8::1]:443")}
	f.os.rows = append(f.os.rows, sockRow{ipproto.TCP, k.src, 1})
	p := &packet.Parsed{IPProto: ipproto.TCP, IPVersion: 6, Src: k.src, Dst: k.dst, TCPFlags: packet.TCPSyn}
	if got := f.r.Classify(p); got != drop {
		t.Errorf("Classify = %v, want drop (gatewayUnavailable)", got)
	}
}
