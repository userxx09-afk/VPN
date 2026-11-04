// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"net/netip"
	"os"
	"sync"
	"time"

	"tappervpn.com/types/ipproto"
)

// sockRow is one row of the OS socket table: a local socket and the PID
// that owns it.
type sockRow struct {
	proto ipproto.Proto // TCP or UDP
	local netip.AddrPort
	pid   uint32
}

type sockKey struct {
	proto ipproto.Proto
	local netip.AddrPort
}

type pathEntry struct {
	path    string
	ok      bool // path lookup succeeded
	expires time.Time
}

// ownerLookup maps the local end of a flow to the app that owns it, using
// periodic snapshots of the OS socket table. It is safe for concurrent use.
type ownerLookup struct {
	// fetch returns the current socket table.
	fetch func() ([]sockRow, error)
	// pathOf returns the image path of a process.
	pathOf func(pid uint32) (string, error)
	now    func() time.Time

	// minRefresh is the minimum time between socket table snapshots, so
	// a burst of lookups for sockets that aren't in the table (yet) doesn't
	// re-read the table for every packet.
	minRefresh time.Duration
	// pathTTL is how long a PID's path is cached. It bounds how long a
	// reused PID can inherit the previous process's path.
	pathTTL time.Duration
	// selfPID is this process's PID. Sockets it owns are reported as
	// [App.Self] without looking up the path.
	selfPID uint32

	mu          sync.Mutex
	lastRefresh time.Time
	socks       map[sockKey]uint32
	paths       map[uint32]pathEntry
}

func newOwnerLookup(fetch func() ([]sockRow, error), pathOf func(uint32) (string, error)) *ownerLookup {
	return &ownerLookup{
		fetch:      fetch,
		pathOf:     pathOf,
		now:        time.Now,
		minRefresh: 10 * time.Millisecond,
		pathTTL:    2 * time.Second,
		selfPID:    uint32(os.Getpid()),
	}
}

// Lookup returns the app owning the local socket src for proto, or nil if
// it can't be determined.
func (o *ownerLookup) Lookup(proto ipproto.Proto, src netip.AddrPort) *App {
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())

	o.mu.Lock()
	defer o.mu.Unlock()

	pid, ok := o.findLocked(proto, src)
	if !ok && o.now().Sub(o.lastRefresh) >= o.minRefresh {
		o.refreshLocked()
		pid, ok = o.findLocked(proto, src)
	}
	if !ok || pid == 0 {
		return nil
	}
	if pid == o.selfPID {
		return &App{Self: true}
	}
	path, ok := o.pathLocked(pid)
	if !ok {
		return nil
	}
	return &App{Path: path}
}

// findLocked looks for an exact match on the local address, then for a
// socket bound to the wildcard address on the same port (typical for
// unconnected UDP). A wildcard IPv6 socket may be dual-stack, so it is
// also checked for IPv4 flows.
func (o *ownerLookup) findLocked(proto ipproto.Proto, src netip.AddrPort) (uint32, bool) {
	if pid, ok := o.socks[sockKey{proto, src}]; ok {
		return pid, true
	}
	port := src.Port()
	if src.Addr().Is4() {
		if pid, ok := o.socks[sockKey{proto, netip.AddrPortFrom(netip.IPv4Unspecified(), port)}]; ok {
			return pid, true
		}
	}
	pid, ok := o.socks[sockKey{proto, netip.AddrPortFrom(netip.IPv6Unspecified(), port)}]
	return pid, ok
}

func (o *ownerLookup) refreshLocked() {
	o.lastRefresh = o.now()
	rows, err := o.fetch()
	if err != nil {
		// Keep the previous snapshot; callers fall back to "unidentified".
		return
	}
	socks := make(map[sockKey]uint32, len(rows))
	for _, r := range rows {
		k := sockKey{r.proto, netip.AddrPortFrom(r.local.Addr().Unmap(), r.local.Port())}
		socks[k] = r.pid
	}
	o.socks = socks
}

func (o *ownerLookup) pathLocked(pid uint32) (string, bool) {
	now := o.now()
	if e, ok := o.paths[pid]; ok && now.Before(e.expires) {
		return e.path, e.ok
	}
	path, err := o.pathOf(pid)
	e := pathEntry{path: path, ok: err == nil, expires: now.Add(o.pathTTL)}
	if o.paths == nil {
		o.paths = make(map[uint32]pathEntry)
	}
	// Bound the cache; PIDs churn on a busy machine.
	if len(o.paths) > 4096 {
		for k, v := range o.paths {
			if !now.Before(v.expires) {
				delete(o.paths, k)
			}
		}
	}
	o.paths[pid] = e
	return e.path, e.ok
}
