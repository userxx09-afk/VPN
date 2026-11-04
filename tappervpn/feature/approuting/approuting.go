// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package approuting decides, per application, whether traffic that would
// go to the exit node goes there, goes through another named gateway, goes
// out the local network directly, or is blocked.
//
// While an exit node is selected, every outgoing packet passes through
// tappervpnd. On the first packet of each flow, Router finds the owning
// process (Windows only, via the OS socket table), matches it against the
// policy, and remembers the decision. "direct" flows are intercepted by
// netstack and re-originated from sockets bound to the physical interface.
// Flows for a named gateway are intercepted the same way and re-originated
// from netstack to the gateway's 4via6 address, which the gateway translates
// back to the IPv4 destination. See docs/per-app-routing.md in the meshwall
// repository.
package approuting

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tappervpn.com/net/packet"
	"tappervpn.com/net/tsaddr"
	"tappervpn.com/types/ipproto"
	"tappervpn.com/types/logger"
	"tappervpn.com/types/nettype"
	"tappervpn.com/wgengine/netstack"
)

// Router classifies local flows and relays the ones it takes over.
type Router struct {
	logf    logger.Logf
	verbose bool // log every new flow's decision

	owners *ownerLookup // nil if this OS has no per-flow owner lookup
	flows  *flowTable
	now    func() time.Time

	// dial opens TCP connections that bypass the tunnel.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// listenPacket opens UDP sockets that bypass the tunnel.
	listenPacket func(ctx context.Context, network, addr string) (net.PacketConn, error)
	// dialViaTCP and dialViaUDP open connections to 4via6 addresses over
	// the mesh, without going through the OS routing table.
	dialViaTCP func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	dialViaUDP func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)

	dialTimeout time.Duration
	udpIdle     time.Duration

	state    atomic.Pointer[routerState]
	gateways atomic.Pointer[map[string]uint32] // lowercased name -> 4via6 site ID
}

type routerState struct {
	matcher *Matcher // nil means app routing is off
	active  bool     // an exit node is selected
}

// RouterOptions configures [NewRouter].
type RouterOptions struct {
	Logf logger.Logf
	// Verbose logs the decision for every new flow.
	Verbose bool
	// Dial must return connections that do not go through the TapperVPN
	// interface (for example a netns dialer).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// ListenPacket must return UDP sockets that do not send through the
	// TapperVPN interface (for example from a netns ListenConfig).
	ListenPacket func(ctx context.Context, network, addr string) (net.PacketConn, error)
	// DialViaTCP and DialViaUDP connect to a 4via6 address over the mesh
	// (for example netstack's DialContextTCP / DialContextUDP). If nil,
	// rules that name a gateway get the policy's gatewayUnavailable action.
	DialViaTCP func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	DialViaUDP func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
}

// NewRouter returns a Router with no policy (app routing off).
func NewRouter(opts RouterOptions) *Router {
	return newRouter(opts, newOSOwnerLookup())
}

func newRouter(opts RouterOptions, owners *ownerLookup) *Router {
	r := &Router{
		logf:         opts.Logf,
		verbose:      opts.Verbose,
		owners:       owners,
		flows:        newFlowTable(),
		now:          time.Now,
		dial:         opts.Dial,
		listenPacket: opts.ListenPacket,
		dialViaTCP:   opts.DialViaTCP,
		dialViaUDP:   opts.DialViaUDP,
		dialTimeout:  15 * time.Second,
		udpIdle:      2 * time.Minute,
	}
	if r.logf == nil {
		r.logf = logger.Discard
	}
	r.state.Store(&routerState{})
	r.gateways.Store(&map[string]uint32{})
	return r
}

// SetMatcher installs the compiled policy. A nil Matcher turns app routing
// off. Flows already in progress keep their decision until they end.
func (r *Router) SetMatcher(m *Matcher) {
	for {
		old := r.state.Load()
		next := &routerState{matcher: m, active: old.active}
		if r.state.CompareAndSwap(old, next) {
			return
		}
	}
}

// SetExitNodeActive records whether an exit node is selected. App routing
// only applies while one is.
func (r *Router) SetExitNodeActive(active bool) {
	for {
		old := r.state.Load()
		next := &routerState{matcher: old.matcher, active: active}
		if r.state.CompareAndSwap(old, next) {
			return
		}
	}
}

// SetGateways sets the gateways that rules can name: node name to the 4via6
// site ID under which that node advertises 0.0.0.0/0. Names are matched
// case-insensitively. New flows use the new set; flows in progress keep
// their gateway.
func (r *Router) SetGateways(gw map[string]uint32) {
	m := make(map[string]uint32, len(gw))
	for name, site := range gw {
		m[strings.ToLower(name)] = site
	}
	r.gateways.Store(&m)
}

func (r *Router) gatewaySite(name string) (uint32, bool) {
	site, ok := (*r.gateways.Load())[strings.ToLower(name)]
	return site, ok
}

// Enabled reports whether a policy is installed and an exit node is active.
func (r *Router) Enabled() bool {
	st := r.state.Load()
	return st.matcher != nil && st.active
}

// Flows returns the currently tracked flows, for debugging.
func (r *Router) Flows() []FlowInfo { return r.flows.snapshot(r.now()) }

// alwaysPassthrough reports whether traffic to dst is never subject to app
// routing: mesh peers (including 4via6 addresses), the TapperVPN service
// IPs, and non-unicast targets.
func alwaysPassthrough(dst netip.Addr) bool {
	return !dst.IsGlobalUnicast() ||
		tsaddr.IsTapperVPNIP(dst) ||
		tsaddr.TapperVPNULARange().Contains(dst) ||
		dst == tsaddr.TapperVPNServiceIP() ||
		dst == tsaddr.TapperVPNServiceIPv6()
}

// Classify is the [netstack.Impl.ClassifyLocalPacket] hook.
func (r *Router) Classify(p *packet.Parsed) netstack.LocalPacketVerdict {
	st := r.state.Load()
	if st.matcher == nil || !st.active {
		return netstack.LocalPacketPassthrough
	}
	if p.IPProto != ipproto.TCP && p.IPProto != ipproto.UDP {
		return netstack.LocalPacketPassthrough
	}
	if alwaysPassthrough(p.Dst.Addr()) {
		return netstack.LocalPacketPassthrough
	}

	now := r.now()
	k := flowKey{p.IPProto, p.Src, p.Dst}
	closing := p.IPProto == ipproto.TCP && p.TCPFlags&(packet.TCPFin|packet.TCPRst) != 0
	e, ok := r.flows.get(k, now, closing)
	if !ok {
		e = r.decide(st.matcher, k, p, now)
	}
	return e.verdict()
}

// verdict maps a flow's decision to what netstack should do with its packets.
func (e flowEntry) verdict() netstack.LocalPacketVerdict {
	switch {
	case e.viaSite:
		return netstack.LocalPacketIntercept
	case e.action == ActionDirect:
		return netstack.LocalPacketIntercept
	case e.action == ActionBlock:
		return netstack.LocalPacketDrop
	}
	return netstack.LocalPacketPassthrough
}

func (r *Router) decide(m *Matcher, k flowKey, p *packet.Parsed, now time.Time) flowEntry {
	// Only the start of a TCP connection is attributed to an app. Any other
	// TCP packet without a flow entry belongs to a connection that predates
	// app routing (or outlived its entry); it gets the default action. If
	// that's "direct", netstack resets it and the app reconnects.
	if k.proto == ipproto.TCP && !p.IsTCPSyn() {
		e := flowEntry{action: m.Default()}
		r.flows.put(k, e, now)
		return e
	}
	var app *App
	if r.owners != nil {
		app = r.owners.Lookup(k.proto, k.src)
	}
	if app != nil && app.Self {
		// tappervpnd's own traffic keeps upstream behavior (passthrough).
		e := flowEntry{action: ActionGateway, rule: "(tappervpnd)"}
		r.flows.put(k, e, now)
		return e
	}
	d := m.Decide(app)
	e := flowEntry{action: d.Action, rule: d.Rule, gateway: d.Gateway}
	if app != nil {
		e.app = app.Path
	}
	if d.Action == ActionGateway && d.Gateway != "" {
		site, ok := r.gatewaySite(d.Gateway)
		switch {
		case !ok, r.dialViaTCP == nil, !k.dst.Addr().Is4():
			// Offline or unknown gateway, or IPv6 (4via6 carries IPv4 only).
			e.action = m.GatewayUnavailable()
			e.gateway = ""
		default:
			e.viaSite = true
			e.site = site
		}
	}
	r.flows.put(k, e, now)
	if r.verbose {
		via := ""
		if e.viaSite {
			via = " via " + d.Gateway
		} else if d.Gateway != "" {
			via = " (gateway " + d.Gateway + " unavailable)"
		}
		r.logf("approuting: %v %v -> %v: %s%s (app %q, rule %q)", k.proto, k.src, k.dst, e.action, via, e.app, d.Rule)
	}
	return e
}

// viaAddr returns the 4via6 address for the IPv4 destination dst behind the
// gateway with the given site ID.
func viaAddr(site uint32, dst netip.AddrPort) (netip.AddrPort, error) {
	p, err := tsaddr.MapVia(site, netip.PrefixFrom(dst.Addr(), 32))
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(p.Addr(), dst.Port()), nil
}

// TCPHandlerForFlow is the [netstack.Impl.GetTCPHandlerForFlow] hook. It
// takes over flows that Classify intercepted and relays them through a
// connection that bypasses the tunnel ("direct") or goes to a named
// gateway. The upstream dial happens before the app's handshake completes,
// so an unreachable destination looks like a refused connection to the app.
func (r *Router) TCPHandlerForFlow(src, dst netip.AddrPort) (handler func(net.Conn), intercept bool) {
	e, ok := r.flows.peek(flowKey{ipproto.TCP, src, dst}, r.now())
	if !ok || e.verdict() != netstack.LocalPacketIntercept {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.dialTimeout)
	defer cancel()
	var upstream net.Conn
	var err error
	if e.viaSite {
		var vdst netip.AddrPort
		if vdst, err = viaAddr(e.site, dst); err == nil {
			upstream, err = r.dialViaTCP(ctx, vdst)
		}
	} else {
		upstream, err = r.dial(ctx, "tcp", dst.String())
	}
	if err != nil {
		if r.verbose {
			r.logf("approuting: dial %v (gateway %q): %v", dst, e.gateway, err)
		}
		return nil, true // reject
	}
	return func(c net.Conn) { splice(c, upstream) }, true
}

// splice copies data both ways until both directions finish, propagating
// half-closes where the connection types support it.
func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

// UDPHandlerForFlow is the [netstack.Impl.GetUDPHandlerForFlow] hook. It
// relays flows that Classify intercepted, either through a UDP socket that
// bypasses the tunnel ("direct") or to a named gateway. Only replies from
// the flow's destination are delivered back to the app.
func (r *Router) UDPHandlerForFlow(src, dst netip.AddrPort) (handler func(nettype.ConnPacketConn), intercept bool) {
	e, ok := r.flows.peek(flowKey{ipproto.UDP, src, dst}, r.now())
	if !ok || e.verdict() != netstack.LocalPacketIntercept {
		return nil, false
	}
	var up udpUpstream
	if e.viaSite {
		vdst, err := viaAddr(e.site, dst)
		if err != nil || r.dialViaUDP == nil {
			return nil, true
		}
		ctx, cancel := context.WithTimeout(context.Background(), r.dialTimeout)
		defer cancel()
		c, err := r.dialViaUDP(ctx, vdst)
		if err != nil {
			r.logf("approuting: UDP to %v via gateway %q: %v", dst, e.gateway, err)
			return nil, true
		}
		up = c
	} else {
		network := "udp4"
		if dst.Addr().Is6() {
			network = "udp6"
		}
		pc, err := r.listenPacket(context.Background(), network, ":0")
		if err != nil {
			r.logf("approuting: direct UDP socket for %v: %v", dst, err)
			return nil, true // reject
		}
		up = &packetUpstream{pc: pc, dst: dst}
	}
	return func(c nettype.ConnPacketConn) { r.relayUDP(c, up) }, true
}

// udpUpstream is the far side of a relayed UDP flow: reads return only
// datagrams from the flow's destination, and writes go to it.
type udpUpstream interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetReadDeadline(time.Time) error
	Close() error
}

// packetUpstream adapts an unconnected socket to udpUpstream for one
// destination, dropping datagrams from anyone else.
type packetUpstream struct {
	pc  net.PacketConn
	dst netip.AddrPort
}

func (u *packetUpstream) Read(b []byte) (int, error) {
	for {
		n, from, err := u.pc.ReadFrom(b)
		if err != nil {
			return 0, err
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		if fp := ua.AddrPort(); netip.AddrPortFrom(fp.Addr().Unmap(), fp.Port()) == u.dst {
			return n, nil
		}
	}
}

func (u *packetUpstream) Write(b []byte) (int, error) {
	return u.pc.WriteTo(b, net.UDPAddrFromAddrPort(u.dst))
}

func (u *packetUpstream) SetReadDeadline(t time.Time) error { return u.pc.SetReadDeadline(t) }
func (u *packetUpstream) Close() error                      { return u.pc.Close() }

func (r *Router) relayUDP(app nettype.ConnPacketConn, upstream udpUpstream) {
	defer app.Close()
	defer upstream.Close()

	var lastActive atomic.Int64
	touch := func() { lastActive.Store(r.now().UnixNano()) }
	touch()
	// idleDone reports whether a read timeout means the whole flow is idle,
	// as opposed to just this direction.
	idleDone := func(err error) bool {
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			return true // real error: stop
		}
		return r.now().Sub(time.Unix(0, lastActive.Load())) >= r.udpIdle
	}
	pump := func(dst interface{ Write([]byte) (int, error) }, src udpUpstream, done chan<- struct{}) {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 64<<10)
		for {
			src.SetReadDeadline(time.Now().Add(r.udpIdle))
			n, err := src.Read(buf)
			if err != nil {
				if idleDone(err) {
					return
				}
				continue
			}
			touch()
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}
	done := make(chan struct{}, 2)
	go pump(upstream, app, done) // app -> upstream
	go pump(app, upstream, done) // upstream -> app
	<-done                       // either direction ending ends the flow
}
