// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"encoding/binary"
	"net/netip"
	"strings"

	"tappervpn.com/net/tsaddr"
	"tappervpn.com/tailcfg"
)

// gatewaySite reports whether p is a 4via6 route for all of IPv4
// (MapVia(site, 0.0.0.0/0)), and if so its site ID.
func gatewaySiteOf(p netip.Prefix) (uint32, bool) {
	if !tsaddr.IsViaPrefix(p) || p.Bits() != 96 {
		return 0, false
	}
	a := p.Addr().As16()
	if a[12] != 0 || a[13] != 0 || a[14] != 0 || a[15] != 0 {
		return 0, false
	}
	return binary.BigEndian.Uint32(a[8:12]), true
}

// nodeNames returns the names a policy can use for n: its MagicDNS name
// without the domain, its computed name, and its OS hostname, lowercased.
func nodeNames(n tailcfg.NodeView) []string {
	var names []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		if s == "" {
			return
		}
		for _, have := range names {
			if have == s {
				return
			}
		}
		names = append(names, s)
	}
	if fqdn := n.Name(); fqdn != "" {
		first, _, _ := strings.Cut(fqdn, ".")
		add(first)
	}
	add(n.ComputedName())
	if hi := n.Hostinfo(); hi.Valid() {
		add(hi.Hostname())
	}
	return names
}

// gatewaySites maps gateway names to 4via6 site IDs for the peers that
// currently serve a gateway route (one of their primary routes is
// MapVia(site, 0.0.0.0/0)). Peers reported offline are skipped. A name that
// matches more than one gateway is left out rather than guessed.
func gatewaySites(peers []tailcfg.NodeView) map[string]uint32 {
	out := map[string]uint32{}
	ambiguous := map[string]bool{}
	for _, p := range peers {
		if online, ok := p.Online().GetOk(); ok && !online {
			continue
		}
		var site uint32
		found := false
		for _, r := range p.PrimaryRoutes().All() {
			if s, ok := gatewaySiteOf(r); ok {
				site, found = s, true
				break
			}
		}
		if !found {
			continue
		}
		for _, name := range nodeNames(p) {
			if prev, ok := out[name]; ok && prev != site {
				ambiguous[name] = true
			}
			out[name] = site
		}
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}
