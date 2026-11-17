// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package ipnlocal

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"

	"tappervpn.com/ipn"
	"tappervpn.com/net/tsaddr"
	"tappervpn.com/tailcfg"
)

// Exit-node failover order (TapperVPN).
//
// The Tapper Dashboard gives each device an ordered list of exit nodes, sent
// from the control server as a node attribute named
//
//	exit-order:tapper,ch-workstation,tapper-standby
//
// A device can also get its own list as "exit-order-device:...", which wins
// over the default (a device matches both: the default targets everyone).
//
// A device follows that list whenever the exit node it is using is one of
// the listed ones (so a person who picks some other exit node, or none, is
// left alone). It uses the first listed exit node that is working, moves
// down the list when that one stops answering, and moves back up when a
// better one answers again.
//
// "Working" is checked from this device, not taken from the control server:
// tapper is both the first exit node and the control server, so when it dies
// nobody is left to say so. The watcher pings the exit nodes over the mesh;
// exitDownAfter misses in a row mark one down, exitUpAfter answers in a row
// bring it back.
const (
	exitOrderCapPrefix       = "exit-order:"
	exitOrderDeviceCapPrefix = "exit-order-device:"
	exitCheckEvery           = 20 * time.Second
	exitPingTimeout          = 5 * time.Second
	exitDownAfter            = 3 // about a minute of silence
	exitUpAfter              = 2
)

// exitHealth is what the watcher knows about one listed exit node.
type exitHealth struct {
	misses, answers int
	down            bool
}

// exitCandidate is a listed exit node as found in the current netmap.
type exitCandidate struct {
	id    tailcfg.StableNodeID
	name  string
	ip    netip.Addr
	exit  bool // currently offers an approved exit route
	known bool // present in the netmap at all
}

// parseExitOrder returns the device's own list if it has one, otherwise the
// default list, from the node attributes in caps.
func parseExitOrder(caps []string) []string {
	parse := func(prefix string) []string {
		for _, c := range caps {
			if !strings.HasPrefix(c, prefix) {
				continue
			}
			var out []string
			for _, n := range strings.Split(strings.TrimPrefix(c, prefix), ",") {
				if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
					out = append(out, n)
				}
			}
			return out
		}
		return nil
	}
	if l := parse(exitOrderDeviceCapPrefix); len(l) > 0 {
		return l
	}
	return parse(exitOrderCapPrefix)
}

// pickOrderedExit returns the first usable candidate, or "" if none is.
func pickOrderedExit(cands []exitCandidate, health map[tailcfg.StableNodeID]*exitHealth) tailcfg.StableNodeID {
	for _, c := range cands {
		if !c.known || !c.exit {
			continue
		}
		if h := health[c.id]; h != nil && h.down {
			continue
		}
		return c.id
	}
	return ""
}

// nodeShortName is the name the dashboard uses for a node ("tapper").
func nodeShortName(n tailcfg.NodeView) string {
	if s := n.ComputedName(); s != "" {
		return strings.ToLower(s)
	}
	s, _, _ := strings.Cut(n.Name(), ".")
	return strings.ToLower(s)
}

// exitOrderLocked returns this device's ordered exit list and the listed
// nodes as currently known. b.mu must be held.
func (b *LocalBackend) exitOrderLocked() ([]string, []exitCandidate) {
	nb := b.currentNode()
	self := nb.Self()
	if !self.Valid() {
		return nil, nil
	}
	var caps []string
	for c := range self.CapMap().All() {
		caps = append(caps, string(c))
	}
	for _, c := range self.Capabilities().All() {
		caps = append(caps, string(c))
	}
	order := parseExitOrder(caps)
	if len(order) == 0 {
		return nil, nil
	}
	byName := map[string]tailcfg.NodeView{}
	for _, p := range nb.Peers() {
		byName[nodeShortName(p)] = p
	}
	cands := make([]exitCandidate, 0, len(order))
	for _, name := range order {
		c := exitCandidate{name: name}
		if p, ok := byName[name]; ok {
			c.known = true
			c.id = p.StableID()
			c.exit = tsaddr.ContainsExitRoutes(p.AllowedIPs())
			for _, a := range p.Addresses().All() {
				if a.Addr().Is4() {
					c.ip = a.Addr()
					break
				}
			}
		}
		cands = append(cands, c)
	}
	return order, cands
}

// exitOrderManagedLocked reports whether prefs' current exit node is one of
// the listed ones, i.e. whether this device follows the order right now.
func exitOrderManagedLocked(prefs *ipn.Prefs, cands []exitCandidate) bool {
	if prefs.ExitNodeID == "" || prefs.AutoExitNode.IsSet() {
		return false
	}
	for _, c := range cands {
		if c.known && c.id == prefs.ExitNodeID {
			return true
		}
	}
	return false
}

// resolveOrderedExitNodeLocked switches prefs to the best listed exit node
// when the device follows the order, and reports whether prefs changed.
// b.mu must be held.
func (b *LocalBackend) resolveOrderedExitNodeLocked(prefs *ipn.Prefs) bool {
	_, cands := b.exitOrderLocked()
	if !exitOrderManagedLocked(prefs, cands) {
		return false
	}
	best := pickOrderedExit(cands, b.exitHealth)
	if best == "" || best == prefs.ExitNodeID {
		return false
	}
	b.logf("exit-order: switching exit node %v -> %v", prefs.ExitNodeID, best)
	prefs.ExitNodeID = best
	prefs.ExitNodeIP = netip.Addr{}
	return true
}

var exitWatcherOnce sync.Once

// startExitOrderWatcher starts the background health checks, once.
func (b *LocalBackend) startExitOrderWatcher() {
	exitWatcherOnce.Do(func() {
		b.goTracker.Go(func() { b.exitOrderWatch(b.ctx) })
	})
}

func (b *LocalBackend) exitOrderWatch(ctx context.Context) {
	t := time.NewTicker(exitCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b.mu.Lock()
		prefs := b.pm.CurrentPrefs().AsStruct()
		_, cands := b.exitOrderLocked()
		managed := exitOrderManagedLocked(prefs, cands)
		b.mu.Unlock()
		if !managed {
			continue
		}
		// Check every listed exit node up to and including the one in use,
		// so a better one coming back is noticed and a dead one is left.
		var check []exitCandidate
		for _, c := range cands {
			if c.known && c.exit && c.ip.IsValid() {
				check = append(check, c)
			}
			if c.id == prefs.ExitNodeID {
				break
			}
		}
		results := make(map[tailcfg.StableNodeID]bool, len(check))
		for _, c := range check {
			pctx, cancel := context.WithTimeout(ctx, exitPingTimeout)
			pr, err := b.Ping(pctx, c.ip, tailcfg.PingDisco, 0)
			cancel()
			results[c.id] = err == nil && pr != nil && pr.Err == ""
		}
		b.mu.Lock()
		if b.exitHealth == nil {
			b.exitHealth = map[tailcfg.StableNodeID]*exitHealth{}
		}
		changed := false
		for _, c := range check {
			h := b.exitHealth[c.id]
			if h == nil {
				h = &exitHealth{}
				b.exitHealth[c.id] = h
			}
			if results[c.id] {
				h.answers++
				h.misses = 0
				if h.down && h.answers >= exitUpAfter {
					h.down = false
					changed = true
					b.logf("exit-order: %s answers again", c.name)
				}
			} else {
				h.misses++
				h.answers = 0
				if !h.down && h.misses >= exitDownAfter {
					h.down = true
					changed = true
					b.logf("exit-order: %s stopped answering", c.name)
				}
			}
		}
		if changed {
			b.refreshExitNodeLocked()
		}
		b.mu.Unlock()
	}
}
