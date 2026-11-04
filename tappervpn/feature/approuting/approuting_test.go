// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"tappervpn.com/net/packet"
	"tappervpn.com/types/ipproto"
	"tappervpn.com/wgengine/netstack"
)

// rawPacket builds a minimal IPv4 TCP, UDP or ICMP packet and parses it.
func rawPacket(t *testing.T, proto ipproto.Proto, src, dst string, flags packet.TCPFlag) *packet.Parsed {
	t.Helper()
	s, d := netip.MustParseAddrPort(src), netip.MustParseAddrPort(dst)
	var l4 []byte
	switch proto {
	case ipproto.TCP:
		l4 = make([]byte, 20)
		binary.BigEndian.PutUint16(l4[0:], s.Port())
		binary.BigEndian.PutUint16(l4[2:], d.Port())
		l4[12] = 5 << 4 // data offset: 5 words
		l4[13] = byte(flags)
	case ipproto.UDP:
		l4 = make([]byte, 8)
		binary.BigEndian.PutUint16(l4[0:], s.Port())
		binary.BigEndian.PutUint16(l4[2:], d.Port())
		binary.BigEndian.PutUint16(l4[4:], 8)
	case ipproto.ICMPv4:
		l4 = make([]byte, 8)
		l4[0] = 8 // echo request
	}
	b := make([]byte, 20+len(l4))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	b[8] = 64
	b[9] = byte(proto)
	s4, d4 := s.Addr().As4(), d.Addr().As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	copy(b[20:], l4)
	p := new(packet.Parsed)
	p.Decode(b)
	if p.IPProto != proto {
		t.Fatalf("decoded proto %v, want %v", p.IPProto, proto)
	}
	return p
}

const routerPolicy = `{
	"version": 1,
	"default": "direct",
	"unidentified": "gateway",
	"rules": [
		{"name": "browsers", "action": "gateway", "apps": [{"windows": {"exe": "chrome.exe"}}]},
		{"name": "no-telemetry", "action": "block", "apps": [{"windows": {"exe": "telemetry.exe"}}]}
	]
}`

type routerFixture struct {
	r     *Router
	os    *fakeOS
	clock time.Time
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	f := &routerFixture{
		os: &fakeOS{
			clock: time.Unix(1000, 0),
			rows: []sockRow{
				{ipproto.TCP, ap("100.64.0.1:40000"), 1},
				{ipproto.TCP, ap("100.64.0.1:40001"), 2},
				{ipproto.TCP, ap("100.64.0.1:40002"), 3},
				{ipproto.UDP, ap("0.0.0.0:5000"), 2},
			},
			paths: map[uint32]string{
				1: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
				2: `C:\Windows\notepad.exe`,
				3: `C:\x\telemetry.exe`,
			},
		},
		clock: time.Unix(1000, 0),
	}
	f.r = newRouter(RouterOptions{}, f.os.lookup())
	f.r.now = func() time.Time { return f.clock }
	p, err := Parse([]byte(routerPolicy))
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Compile(CompileOptions{GOOS: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	f.r.SetMatcher(m)
	f.r.SetExitNodeActive(true)
	return f
}

const (
	pass      = netstack.LocalPacketPassthrough
	intercept = netstack.LocalPacketIntercept
	drop      = netstack.LocalPacketDrop
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name  string
		proto ipproto.Proto
		src   string
		dst   string
		flags packet.TCPFlag
		want  netstack.LocalPacketVerdict
	}{
		{"gateway app", ipproto.TCP, "100.64.0.1:40000", "1.1.1.1:443", packet.TCPSyn, pass},
		{"default direct", ipproto.TCP, "100.64.0.1:40001", "1.1.1.1:443", packet.TCPSyn, intercept},
		{"blocked app", ipproto.TCP, "100.64.0.1:40002", "1.1.1.1:443", packet.TCPSyn, drop},
		{"unidentified", ipproto.TCP, "100.64.0.1:49999", "1.1.1.1:443", packet.TCPSyn, pass},
		{"udp wildcard owner", ipproto.UDP, "100.64.0.1:5000", "8.8.8.8:53", 0, intercept},
		{"non-syn without flow gets default", ipproto.TCP, "100.64.0.1:40000", "9.9.9.9:443", packet.TCPAck, intercept},
		{"tailnet peer untouched", ipproto.TCP, "100.64.0.1:40002", "100.64.0.2:22", packet.TCPSyn, pass},
		{"service ip untouched", ipproto.UDP, "100.64.0.1:5000", "100.100.100.100:53", 0, pass},
		{"multicast untouched", ipproto.UDP, "100.64.0.1:5000", "224.0.0.251:5353", 0, pass},
		{"icmp untouched", ipproto.ICMPv4, "100.64.0.1:0", "1.1.1.1:0", 0, pass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t)
			p := rawPacket(t, tt.proto, tt.src, tt.dst, tt.flags)
			if got := f.r.Classify(p); got != tt.want {
				t.Errorf("Classify = %v, want %v", got, tt.want)
			}
		})
	}
}

// Flows owned by tappervpnd itself pass through, even when the default
// action would re-originate them (which would loop back into the tunnel).
func TestClassifySelf(t *testing.T) {
	f := newRouterFixture(t)
	f.r.owners.selfPID = 2 // owns TCP :40001 and UDP :5000, otherwise default direct
	tests := []struct {
		proto ipproto.Proto
		src   string
		flags packet.TCPFlag
	}{
		{ipproto.TCP, "100.64.0.1:40001", packet.TCPSyn},
		{ipproto.UDP, "100.64.0.1:5000", 0},
	}
	for _, tt := range tests {
		p := rawPacket(t, tt.proto, tt.src, "1.1.1.1:3478", tt.flags)
		if got := f.r.Classify(p); got != pass {
			t.Errorf("%v %s: Classify = %v, want passthrough", tt.proto, tt.src, got)
		}
	}
}

func TestClassifyInactive(t *testing.T) {
	f := newRouterFixture(t)
	p := rawPacket(t, ipproto.TCP, "100.64.0.1:40002", "1.1.1.1:443", packet.TCPSyn)

	f.r.SetExitNodeActive(false)
	if got := f.r.Classify(p); got != pass {
		t.Errorf("no exit node: Classify = %v, want passthrough", got)
	}
	if f.r.Enabled() {
		t.Error("Enabled with no exit node")
	}

	f.r.SetExitNodeActive(true)
	f.r.SetMatcher(nil)
	if got := f.r.Classify(p); got != pass {
		t.Errorf("no policy: Classify = %v, want passthrough", got)
	}
	if f.os.fetches != 0 {
		t.Errorf("owner lookups while disabled: %d", f.os.fetches)
	}
}

func TestClassifyCachesFlow(t *testing.T) {
	f := newRouterFixture(t)
	syn := rawPacket(t, ipproto.TCP, "100.64.0.1:40001", "1.1.1.1:443", packet.TCPSyn)
	ack := rawPacket(t, ipproto.TCP, "100.64.0.1:40001", "1.1.1.1:443", packet.TCPAck)
	fin := rawPacket(t, ipproto.TCP, "100.64.0.1:40001", "1.1.1.1:443", packet.TCPFin|packet.TCPAck)

	if got := f.r.Classify(syn); got != intercept {
		t.Fatalf("SYN: %v, want intercept", got)
	}
	fetches := f.os.fetches

	// The app's owner changes in the socket table; the cached decision holds.
	f.os.paths[2] = `C:\Program Files\Google\Chrome\Application\chrome.exe`
	f.clock = f.clock.Add(time.Second)
	if got := f.r.Classify(ack); got != intercept {
		t.Fatalf("ACK: %v, want cached intercept", got)
	}
	if f.os.fetches != fetches {
		t.Fatalf("owner lookup repeated for a known flow")
	}

	// After FIN the flow lingers briefly, then expires.
	if got := f.r.Classify(fin); got != intercept {
		t.Fatalf("FIN: %v, want intercept", got)
	}
	if got := len(f.r.Flows()); got != 1 {
		t.Fatalf("Flows() has %d entries, want 1", got)
	}
	f.clock = f.clock.Add(f.r.flows.tcpClosing)
	if got := len(f.r.Flows()); got != 0 {
		t.Fatalf("Flows() has %d entries after close timeout, want 0", got)
	}
}

func TestFlowTableExpiry(t *testing.T) {
	ft := newFlowTable()
	now := time.Unix(0, 0)
	tcp := flowKey{ipproto.TCP, ap("100.64.0.1:1"), ap("1.1.1.1:443")}
	udp := flowKey{ipproto.UDP, ap("100.64.0.1:2"), ap("1.1.1.1:53")}
	ft.put(tcp, flowEntry{action: ActionDirect}, now)
	ft.put(udp, flowEntry{action: ActionBlock}, now)

	if _, ok := ft.get(udp, now.Add(ft.udpIdle-time.Second), false); !ok {
		t.Fatal("UDP flow expired early")
	}
	// get refreshed it, so it survives another almost-full idle period.
	if _, ok := ft.peek(udp, now.Add(2*ft.udpIdle-2*time.Second)); !ok {
		t.Fatal("UDP flow not refreshed by get")
	}
	if _, ok := ft.peek(udp, now.Add(2*ft.udpIdle)); ok {
		t.Fatal("UDP flow did not expire")
	}
	if _, ok := ft.peek(tcp, now.Add(ft.tcpIdle-time.Second)); !ok {
		t.Fatal("TCP flow expired early")
	}

	// Sweeping removes expired entries from the map.
	ft.put(flowKey{ipproto.UDP, ap("100.64.0.1:3"), ap("1.1.1.1:53")}, flowEntry{}, now.Add(time.Hour))
	if n := len(ft.m); n != 1 {
		t.Fatalf("after sweep map has %d entries, want 1", n)
	}
}

func TestTCPHandlerForFlow(t *testing.T) {
	// A local echo server stands in for the internet.
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
				c.Write([]byte("echo:" + line))
			}()
		}
	}()

	f := newRouterFixture(t)
	var dialed string
	f.r.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		if addr == "1.1.1.1:443" {
			return nil, errors.New("unreachable")
		}
		return net.Dial("tcp", ln.Addr().String())
	}

	src := ap("100.64.0.1:40001")
	echoDst := ap("203.0.113.5:80")

	// Not a classified flow: not ours.
	if h, ok := f.r.TCPHandlerForFlow(src, echoDst); h != nil || ok {
		t.Fatalf("unclassified flow: got (%v, %v), want (nil, false)", h != nil, ok)
	}

	// A gateway flow is not ours either.
	f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40000", "203.0.113.5:80", packet.TCPSyn))
	if h, ok := f.r.TCPHandlerForFlow(ap("100.64.0.1:40000"), echoDst); h != nil || ok {
		t.Fatalf("gateway flow: got (%v, %v), want (nil, false)", h != nil, ok)
	}

	// A direct flow whose upstream dial fails is rejected.
	f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40001", "1.1.1.1:443", packet.TCPSyn))
	if h, ok := f.r.TCPHandlerForFlow(src, ap("1.1.1.1:443")); h != nil || !ok {
		t.Fatalf("failed dial: got (%v, %v), want (nil, true)", h != nil, ok)
	}

	// A direct flow is relayed.
	f.r.Classify(rawPacket(t, ipproto.TCP, "100.64.0.1:40001", "203.0.113.5:80", packet.TCPSyn))
	h, ok := f.r.TCPHandlerForFlow(src, echoDst)
	if h == nil || !ok {
		t.Fatalf("direct flow: got (%v, %v), want handler", h != nil, ok)
	}
	if dialed != "203.0.113.5:80" {
		t.Errorf("dialed %q, want the flow's destination", dialed)
	}
	appSide, nsSide := net.Pipe()
	go h(nsSide)
	appSide.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := appSide.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(appSide).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got != "echo:hi\n" {
		t.Errorf("relayed %q, want %q", got, "echo:hi\n")
	}
	appSide.Close()
}

func TestUDPHandlerForFlow(t *testing.T) {
	// A local UDP echo server stands in for the internet.
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
			echo.WriteTo(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	echoAddr := echo.LocalAddr().(*net.UDPAddr).AddrPort()

	f := newRouterFixture(t)
	f.r.listenPacket = func(ctx context.Context, network, addr string) (net.PacketConn, error) {
		return net.ListenPacket(network, "127.0.0.1:0")
	}

	src := ap("100.64.0.1:5000")
	if h, ok := f.r.UDPHandlerForFlow(src, echoAddr); h != nil || ok {
		t.Fatal("unclassified UDP flow intercepted")
	}
	// Classify a flow to the echo server's real address. 127.0.0.1 is not
	// global unicast, so record the decision directly.
	f.r.flows.put(flowKey{ipproto.UDP, src, echoAddr}, flowEntry{action: ActionDirect}, f.clock)
	h, ok := f.r.UDPHandlerForFlow(src, echoAddr)
	if h == nil || !ok {
		t.Fatal("direct UDP flow not intercepted")
	}

	// The "app" is a plain UDP socket; netstack's side of the flow is a UDP
	// socket connected to it (a *net.UDPConn is a ConnPacketConn).
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
	if _, err := appSide.WriteTo([]byte("ping"), nsConn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 100)
	n, _, err := appSide.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "echo:ping" {
		t.Errorf("relayed %q, want %q", got, "echo:ping")
	}
}
