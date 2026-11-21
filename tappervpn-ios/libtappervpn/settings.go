// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"encoding/json"
	"net/netip"
	"sync"

	"tappervpn.com/net/dns"
	"tappervpn.com/wgengine/router"
)

// NetworkSettings is what Swift turns into NEPacketTunnelNetworkSettings.
// IPv4 routes and addresses carry dotted masks and IPv6 ones prefix
// lengths, which is what NEIPv4Route / NEIPv6Route take.
type NetworkSettings struct {
	MTU               int        `json:"mtu"`
	IPv4Addresses     []IPv4     `json:"ipv4Addresses"`
	IPv6Addresses     []IPv6     `json:"ipv6Addresses"`
	IPv4Routes        []IPv4     `json:"ipv4Routes"`
	IPv4ExcludeRoutes []IPv4     `json:"ipv4ExcludeRoutes"`
	IPv6Routes        []IPv6     `json:"ipv6Routes"`
	IPv6ExcludeRoutes []IPv6     `json:"ipv6ExcludeRoutes"`
	DNS               *DNSConfig `json:"dns,omitempty"`
}

type IPv4 struct {
	Address string `json:"address"`
	Mask    string `json:"mask"`
}

type IPv6 struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefixLength"`
}

type DNSConfig struct {
	Servers       []string `json:"servers"`
	SearchDomains []string `json:"searchDomains"`
	// MatchDomains: the servers answer only these names. Empty means
	// they answer everything (Swift sets matchDomains to [""]).
	MatchDomains []string `json:"matchDomains"`
}

func (s *NetworkSettings) marshal() (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}

func v4(p netip.Prefix) IPv4 {
	p = p.Masked()
	var mask [4]byte
	for i := 0; i < p.Bits(); i++ {
		mask[i/8] |= 0x80 >> (i % 8)
	}
	return IPv4{Address: p.Addr().String(), Mask: netip.AddrFrom4(mask).String()}
}

func v6(p netip.Prefix) IPv6 {
	p = p.Masked()
	return IPv6{Address: p.Addr().String(), PrefixLength: p.Bits()}
}

// makeSettings converts the engine's router and DNS config. Our own
// addresses are not masked: iOS wants the interface address itself.
func makeSettings(rcfg *router.Config, dcfg *dns.OSConfig) *NetworkSettings {
	s := &NetworkSettings{
		MTU:               mtu,
		IPv4Addresses:     []IPv4{},
		IPv6Addresses:     []IPv6{},
		IPv4Routes:        []IPv4{},
		IPv4ExcludeRoutes: []IPv4{},
		IPv6Routes:        []IPv6{},
		IPv6ExcludeRoutes: []IPv6{},
	}
	if rcfg == nil {
		return s
	}
	for _, a := range rcfg.LocalAddrs {
		if a.Addr().Is4() {
			s.IPv4Addresses = append(s.IPv4Addresses, IPv4{Address: a.Addr().String(), Mask: "255.255.255.255"})
		} else {
			s.IPv6Addresses = append(s.IPv6Addresses, IPv6{Address: a.Addr().String(), PrefixLength: 128})
		}
	}
	for _, r := range rcfg.Routes {
		if r.Addr().Is4() {
			s.IPv4Routes = append(s.IPv4Routes, v4(r))
		} else {
			s.IPv6Routes = append(s.IPv6Routes, v6(r))
		}
	}
	for _, r := range rcfg.LocalRoutes {
		if r.Addr().IsLoopback() {
			continue
		}
		if r.Addr().Is4() {
			s.IPv4ExcludeRoutes = append(s.IPv4ExcludeRoutes, v4(r))
		} else {
			s.IPv6ExcludeRoutes = append(s.IPv6ExcludeRoutes, v6(r))
		}
	}
	if dcfg != nil && len(dcfg.Nameservers) > 0 {
		d := &DNSConfig{Servers: []string{}, SearchDomains: []string{}, MatchDomains: []string{}}
		for _, ns := range dcfg.Nameservers {
			d.Servers = append(d.Servers, ns.String())
		}
		for _, sd := range dcfg.SearchDomains {
			d.SearchDomains = append(d.SearchDomains, sd.WithoutTrailingDot())
		}
		for _, md := range dcfg.MatchDomains {
			d.MatchDomains = append(d.MatchDomains, md.WithoutTrailingDot())
		}
		s.DNS = d
	}
	return s
}

var (
	_ router.Router      = (*vpnFacade)(nil)
	_ dns.OSConfigurator = (*vpnFacade)(nil)
)

// vpnFacade is the engine's router and DNS configurator. It keeps the last
// config of each; the engine then calls reconfigure, which hands both to
// iOS at once (as on Android).
type vpnFacade struct {
	apply func(*router.Config, *dns.OSConfig) error

	mu   sync.Mutex
	rcfg *router.Config
	dcfg *dns.OSConfig
}

func (vf *vpnFacade) Up() error { return nil }

func (vf *vpnFacade) Set(rcfg *router.Config) error {
	vf.mu.Lock()
	defer vf.mu.Unlock()
	vf.rcfg = rcfg
	return nil
}

func (vf *vpnFacade) SetDNS(dcfg dns.OSConfig) error {
	vf.mu.Lock()
	defer vf.mu.Unlock()
	vf.dcfg = &dcfg
	return nil
}

// SupportsSplitDNS: NEDNSSettings.matchDomains sends only the tailnet's
// names to MagicDNS and leaves the rest to the network's resolver.
func (vf *vpnFacade) SupportsSplitDNS() bool { return true }

func (vf *vpnFacade) GetBaseConfig() (dns.OSConfig, error) {
	return dns.OSConfig{}, dns.ErrGetBaseConfigNotSupported
}

func (vf *vpnFacade) Close() error { return nil }

func (vf *vpnFacade) reconfigure() error {
	vf.mu.Lock()
	rcfg, dcfg := vf.rcfg, vf.dcfg
	vf.mu.Unlock()
	if rcfg == nil {
		return nil
	}
	return vf.apply(rcfg, dcfg)
}
