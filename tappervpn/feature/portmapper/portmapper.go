// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package portmapper registers support for NAT-PMP, PCP, and UPnP port
// mapping protocols to help get direction connections through NATs.
package portmapper

import (
	"tappervpn.com/feature"
	"tappervpn.com/net/netmon"
	"tappervpn.com/net/portmapper"
	"tappervpn.com/net/portmapper/portmappertype"
	"tappervpn.com/types/logger"
	"tappervpn.com/util/eventbus"
)

func init() {
	if !feature.Register("portmapper") {
		return
	}
	portmappertype.HookNewPortMapper.Set(newPortMapper)
}

func newPortMapper(
	logf logger.Logf,
	bus *eventbus.Bus,
	netMon *netmon.Monitor,
	disableUPnPOrNil func() bool,
	onlyTCP443OrNil func() bool) portmappertype.Client {

	pm := portmapper.NewClient(portmapper.Config{
		EventBus: bus,
		Logf:     logf,
		NetMon:   netMon,
		DebugKnobs: &portmapper.DebugKnobs{
			DisableAll:      onlyTCP443OrNil,
			DisableUPnPFunc: disableUPnPOrNil,
		},
	})
	pm.SetGatewayLookupFunc(netMon.GatewayAndSelfIP)
	return pm
}
