// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package dns

import (
	"tappervpn.com/control/controlknobs"
	"tappervpn.com/health"
	"tappervpn.com/types/logger"
	"tappervpn.com/util/eventbus"
	"tappervpn.com/util/syspolicy/policyclient"
)

func NewOSConfigurator(logf logger.Logf, health *health.Tracker, bus *eventbus.Bus, _ policyclient.Client, _ *controlknobs.Knobs, iface string) (OSConfigurator, error) {
	return newDirectManager(logf, health, bus), nil
}
