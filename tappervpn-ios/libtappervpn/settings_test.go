// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"net/netip"
	"reflect"
	"testing"

	"tappervpn.com/net/dns"
	"tappervpn.com/util/dnsname"
	"tappervpn.com/wgengine/router"
)

func TestMakeSettings(t *testing.T) {
	p := netip.MustParsePrefix
	rcfg := &router.Config{
		LocalAddrs:  []netip.Prefix{p("100.64.0.7/32"), p("fd7a:115c:a1e0::7/128")},
		Routes:      []netip.Prefix{p("100.64.0.0/10"), p("0.0.0.0/0"), p("fd7a:115c:a1e0::/48"), p("10.1.2.3/20")},
		LocalRoutes: []netip.Prefix{p("192.168.1.0/24"), p("127.0.0.0/8")},
	}
	dcfg := &dns.OSConfig{
		Nameservers:   []netip.Addr{netip.MustParseAddr("100.100.100.100")},
		SearchDomains: []dnsname.FQDN{"tapper.net."},
		MatchDomains:  []dnsname.FQDN{"tapper.net."},
	}
	s := makeSettings(rcfg, dcfg)

	if want := []IPv4{{"100.64.0.7", "255.255.255.255"}}; !reflect.DeepEqual(s.IPv4Addresses, want) {
		t.Errorf("IPv4Addresses = %v, want %v", s.IPv4Addresses, want)
	}
	if want := []IPv6{{"fd7a:115c:a1e0::7", 128}}; !reflect.DeepEqual(s.IPv6Addresses, want) {
		t.Errorf("IPv6Addresses = %v, want %v", s.IPv6Addresses, want)
	}
	wantRoutes := []IPv4{{"100.64.0.0", "255.192.0.0"}, {"0.0.0.0", "0.0.0.0"}, {"10.1.0.0", "255.255.240.0"}}
	if !reflect.DeepEqual(s.IPv4Routes, wantRoutes) {
		t.Errorf("IPv4Routes = %v, want %v", s.IPv4Routes, wantRoutes)
	}
	if want := []IPv4{{"192.168.1.0", "255.255.255.0"}}; !reflect.DeepEqual(s.IPv4ExcludeRoutes, want) {
		t.Errorf("IPv4ExcludeRoutes = %v, want %v (loopback dropped)", s.IPv4ExcludeRoutes, want)
	}
	if want := []IPv6{{"fd7a:115c:a1e0::", 48}}; !reflect.DeepEqual(s.IPv6Routes, want) {
		t.Errorf("IPv6Routes = %v, want %v", s.IPv6Routes, want)
	}
	if s.DNS == nil || !reflect.DeepEqual(s.DNS.MatchDomains, []string{"tapper.net"}) || s.DNS.Servers[0] != "100.100.100.100" {
		t.Errorf("DNS = %+v", s.DNS)
	}

	if empty := makeSettings(nil, nil); len(empty.IPv4Addresses) != 0 || empty.DNS != nil {
		t.Errorf("nil config gave %+v", empty)
	}
}
