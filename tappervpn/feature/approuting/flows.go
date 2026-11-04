// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"net/netip"
	"sync"
	"time"

	"tappervpn.com/types/ipproto"
)

type flowKey struct {
	proto    ipproto.Proto
	src, dst netip.AddrPort
}

type flowEntry struct {
	action  Action
	rule    string // name of the matching rule, or ""
	app     string // owning app's path, or "" if unidentified
	gateway string // named gateway, if the flow uses one
	viaSite bool   // relayed to a named gateway's 4via6 site
	site    uint32 // the gateway's 4via6 site ID, if viaSite
	last    time.Time
	closing bool // TCP FIN or RST seen
}

// flowTable remembers the decision for each flow so that only a flow's
// first packet pays for owner lookup and rule matching, and so that all
// packets of a flow take the same path. It is safe for concurrent use.
type flowTable struct {
	tcpIdle    time.Duration // idle timeout for open TCP flows
	tcpClosing time.Duration // how long a TCP flow lingers after FIN/RST
	udpIdle    time.Duration // idle timeout for UDP flows
	maxFlows   int           // sweep early above this many flows

	mu        sync.Mutex
	m         map[flowKey]*flowEntry
	lastSweep time.Time
}

func newFlowTable() *flowTable {
	return &flowTable{
		tcpIdle:    10 * time.Minute,
		tcpClosing: 10 * time.Second,
		udpIdle:    2 * time.Minute,
		maxFlows:   65536,
		m:          make(map[flowKey]*flowEntry),
	}
}

func (t *flowTable) expired(k flowKey, e *flowEntry, now time.Time) bool {
	idle := t.udpIdle
	if k.proto == ipproto.TCP {
		idle = t.tcpIdle
		if e.closing {
			idle = t.tcpClosing
		}
	}
	return now.Sub(e.last) >= idle
}

// get returns the entry for k and refreshes its idle timer. If closing is
// set, the flow is marked as closing (TCP FIN or RST).
func (t *flowTable) get(k flowKey, now time.Time, closing bool) (flowEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[k]
	if !ok || t.expired(k, e, now) {
		return flowEntry{}, false
	}
	e.last = now
	if closing {
		e.closing = true
	}
	return *e, true
}

// peek returns the entry for k without refreshing it.
func (t *flowTable) peek(k flowKey, now time.Time) (flowEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[k]
	if !ok || t.expired(k, e, now) {
		return flowEntry{}, false
	}
	return *e, true
}

func (t *flowTable) put(k flowKey, e flowEntry, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.lastSweep) >= 5*time.Second || len(t.m) >= t.maxFlows {
		t.sweepLocked(now)
	}
	e.last = now
	t.m[k] = &e
}

func (t *flowTable) sweepLocked(now time.Time) {
	t.lastSweep = now
	for k, e := range t.m {
		if t.expired(k, e, now) {
			delete(t.m, k)
		}
	}
}

// FlowInfo describes one tracked flow, for debugging.
type FlowInfo struct {
	Proto    string
	Src, Dst netip.AddrPort
	Action   Action
	Rule     string
	App      string
	Gateway  string
	Idle     time.Duration
}

func (t *flowTable) snapshot(now time.Time) []FlowInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FlowInfo, 0, len(t.m))
	for k, e := range t.m {
		if t.expired(k, e, now) {
			continue
		}
		out = append(out, FlowInfo{
			Proto:   k.proto.String(),
			Src:     k.src,
			Dst:     k.dst,
			Action:  e.action,
			Rule:    e.rule,
			App:     e.app,
			Gateway: e.gateway,
			Idle:    now.Sub(e.last),
		})
	}
	return out
}
