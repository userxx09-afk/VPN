// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"context"
	"encoding/json"
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
	"tappervpn.com/ipn/localapi"
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
	localapi.Register("app-routing", serveLocalAPI)
}

// localStateKey is where the policy set in the TapperVPN app (LocalAPI
// "app-routing") is kept in tappervpnd's state store.
const localStateKey = ipn.StateKey("_tappervpn-app-routing")

type extension struct {
	logf   logger.Logf
	router *Router
	host   ipnext.Host    // set in Init
	store  ipn.StateStore // may be nil

	mu         sync.Mutex
	controlDoc []string // policy documents from the control server
	localDoc   []byte   // policy set on this device in the TapperVPN app, or nil
	source     string   // where the active policy came from: "control", "app", a file path, or ""
	policyErr  string   // why the policy was rejected, or ""
	exitActive bool     // an exit node is selected (app routing only applies then)
	lastErr    string   // last policy message logged, to avoid repeating it
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
	if st, ok := sys.StateStore.GetOK(); ok {
		e.store = st
	}
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
	if e.store != nil {
		if b, err := e.store.ReadState(localStateKey); err == nil && len(b) > 0 {
			e.localDoc = b
		}
	}
	e.applyLocked()
	return nil
}

// setLocal validates, saves and applies the policy set in the TapperVPN
// app. A nil doc removes it (every app follows the exit node).
func (e *extension) setLocal(doc []byte) error {
	if doc != nil {
		p, err := Merge([][]byte{doc})
		if err != nil {
			return err
		}
		if p != nil {
			if _, err := p.Compile(CompileOptions{GOOS: runtime.GOOS}); err != nil {
				return err
			}
		}
	}
	if e.store != nil {
		if err := ipn.WriteState(e.store, localStateKey, doc); err != nil {
			return fmt.Errorf("saving: %w", err)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.localDoc = doc
	e.applyLocked()
	return nil
}

// status is what the TapperVPN app shows about per-app routing.
type status struct {
	Policy     json.RawMessage `json:"policy"`     // the policy set in the app, or null
	Source     string          `json:"source"`     // where the active policy came from ("control", "app", a file, "")
	Error      string          `json:"error"`      // why the active policy was rejected
	ExitNode   bool            `json:"exitNode"`   // an exit node is selected; app routing only applies then
	ControlSet bool            `json:"controlSet"` // the control server sends a policy, which overrides the app's
}

func (e *extension) status() status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := status{Source: e.source, Error: e.policyErr, ExitNode: e.exitActive, ControlSet: len(e.controlDoc) > 0}
	if e.localDoc != nil {
		st.Policy = json.RawMessage(e.localDoc)
	}
	return st
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
	e.mu.Lock()
	e.exitActive = active
	e.mu.Unlock()
}

// applyLocked compiles and installs the current policy: the control
// server's if it sent one, else the one set in the TapperVPN app, else the
// local file if configured. On error, app routing is turned off rather
// than left on a stale policy.
func (e *extension) applyLocked() {
	var raw [][]byte
	source := "control"
	e.policyErr = ""
	e.source = ""
	for _, d := range e.controlDoc {
		raw = append(raw, []byte(d))
	}
	if len(raw) == 0 && e.localDoc != nil {
		raw = [][]byte{e.localDoc}
		source = "app"
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
		e.policyErr = err.Error()
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
		e.policyErr = err.Error()
		e.reportLocked("policy from %s rejected, app routing off: %v", source, err)
		e.router.SetMatcher(nil)
		return
	}
	e.source = source
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
