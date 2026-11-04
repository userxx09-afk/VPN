// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sync"

	"tappervpn.com/envknob"
	"tappervpn.com/feature"
	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnext"
	"tappervpn.com/net/netns"
	"tappervpn.com/net/packet"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
	"tappervpn.com/types/nettype"
	"tappervpn.com/wgengine/netstack"
)

const featureName = "approuting"

var (
	// policyFileEnv names a local policy file, used only when the control
	// server sends no policy. For development.
	policyFileEnv = envknob.RegisterString("TS_DEBUG_APPROUTING_FILE")
	// verboseEnv logs the decision for every new flow.
	verboseEnv = envknob.RegisterBool("TS_DEBUG_APPROUTING")
)

func init() {
	if !feature.Register(featureName) {
		return
	}
	ipnext.RegisterExtension(featureName, newExtension)
}

type extension struct {
	logf   logger.Logf
	router *Router
	host   ipnext.Host // set in Init

	mu         sync.Mutex
	controlDoc []string // policy documents from the control server
	lastErr    string   // last policy error logged, to avoid repeating it
}

func newExtension(logf logger.Logf, b ipnext.SafeBackend) (ipnext.Extension, error) {
	sys := b.Sys()
	if sys.IsNetstack() {
		// Userspace-networking mode: the OS doesn't route traffic through
		// tappervpnd, so there's nothing to classify.
		return nil, ipnext.SkipExtension
	}
	impl, ok := sys.Netstack.GetOK()
	if !ok {
		return nil, ipnext.SkipExtension
	}
	ns, ok := impl.(*netstack.Impl)
	if !ok {
		return nil, ipnext.SkipExtension
	}
	logf = logger.WithPrefix(logf, "approuting: ")
	netMon := sys.NetMon.Get()
	d := netns.NewDialer(logf, netMon)
	lc := netns.Listener(logf, netMon)

	e := &extension{logf: logf}
	e.router = NewRouter(RouterOptions{
		Logf:         logf,
		Verbose:      verboseEnv(),
		Dial:         d.DialContext,
		ListenPacket: lc.ListenPacket,
		// Named-gateway flows are re-originated inside netstack, so they go
		// straight to WireGuard and can never leak out the physical NIC.
		// Don't return a typed nil *gonet conn as a non-nil net.Conn.
		DialViaTCP: func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
			c, err := ns.DialContextTCP(ctx, dst)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		DialViaUDP: func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
			c, err := ns.DialContextUDP(ctx, dst)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	})
	installHooks(ns, e.router)
	return e, nil
}

// installHooks attaches r to ns. It runs before netstack starts, so the
// plain field writes don't race with the datapath. Existing flow handlers
// (none in tappervpnd today) still get flows that r doesn't take.
func installHooks(ns *netstack.Impl, r *Router) {
	ns.ClassifyLocalPacket = func(p *packet.Parsed) netstack.LocalPacketVerdict {
		return r.Classify(p)
	}
	prevTCP := ns.GetTCPHandlerForFlow
	ns.GetTCPHandlerForFlow = func(src, dst netip.AddrPort) (func(net.Conn), bool) {
		if h, ok := r.TCPHandlerForFlow(src, dst); ok {
			return h, true
		}
		if prevTCP != nil {
			return prevTCP(src, dst)
		}
		return nil, false
	}
	prevUDP := ns.GetUDPHandlerForFlow
	ns.GetUDPHandlerForFlow = func(src, dst netip.AddrPort) (func(nettype.ConnPacketConn), bool) {
		if h, ok := r.UDPHandlerForFlow(src, dst); ok {
			return h, true
		}
		if prevUDP != nil {
			return prevUDP(src, dst)
		}
		return nil, false
	}
}

func (*extension) Name() string { return featureName }

func (e *extension) Init(h ipnext.Host) error {
	e.host = h
	h.Hooks().OnSelfChange.Add(e.onSelfChange)
	h.Hooks().OnPeerUpdate.Add(e.refreshGateways)
	h.Hooks().ProfileStateChange.Add(e.onProfileStateChange)
	e.onProfileStateChange(ipn.LoginProfileView{}, h.Profiles().CurrentPrefs(), true)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.applyLocked()
	return nil
}

func (e *extension) Shutdown() error {
	e.router.SetMatcher(nil)
	return nil
}

func (e *extension) onSelfChange(self tailcfg.NodeView) {
	var docs []string
	if self.Valid() {
		for _, v := range self.CapMap().Get(tailcfg.NodeCapability(CapName)).All() {
			docs = append(docs, string(v))
		}
	}
	e.mu.Lock()
	e.controlDoc = docs
	e.applyLocked()
	e.mu.Unlock()
	e.refreshGateways()
}

// refreshGateways recomputes which peers can serve as named gateways. It
// runs from extension hooks, where reading the node backend is safe.
func (e *extension) refreshGateways() {
	if e.host == nil {
		return
	}
	e.router.SetGateways(gatewaySites(e.host.NodeBackend().Peers()))
}

func (e *extension) onProfileStateChange(_ ipn.LoginProfileView, prefs ipn.PrefsView, _ bool) {
	active := prefs.Valid() && (prefs.ExitNodeID() != "" || prefs.ExitNodeIP().IsValid())
	e.router.SetExitNodeActive(active)
}

// applyLocked compiles and installs the current policy: the control
// server's if it sent one, else the local file if configured. On error,
// app routing is turned off rather than left on a stale policy.
func (e *extension) applyLocked() {
	var raw [][]byte
	source := "control"
	for _, d := range e.controlDoc {
		raw = append(raw, []byte(d))
	}
	if len(raw) == 0 {
		if path := policyFileEnv(); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				e.reportLocked("reading %s: %v", path, err)
				e.router.SetMatcher(nil)
				return
			}
			raw = [][]byte{b}
			source = path
		}
	}
	p, err := Merge(raw)
	if err != nil {
		e.reportLocked("policy from %s rejected, app routing off: %v", source, err)
		e.router.SetMatcher(nil)
		return
	}
	if p == nil {
		e.router.SetMatcher(nil)
		return
	}
	m, err := p.Compile(CompileOptions{GOOS: runtime.GOOS})
	if err != nil {
		e.reportLocked("policy from %s rejected, app routing off: %v", source, err)
		e.router.SetMatcher(nil)
		return
	}
	e.router.SetMatcher(m)
	e.reportLocked("policy from %s active: %d rules, default %s", source, len(p.Rules), m.Default())
}

func (e *extension) reportLocked(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if s == e.lastErr {
		return
	}
	e.lastErr = s
	e.logf("%s", s)
}
