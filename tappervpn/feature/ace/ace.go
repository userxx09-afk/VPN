// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package ace registers support for Alternate Connectivity Endpoints (ACE).
package ace

import (
	"net/netip"

	"tappervpn.com/control/controlhttp"
	"tappervpn.com/feature"
	"tappervpn.com/net/ace"
	"tappervpn.com/net/netx"
)

func init() {
	if !feature.Register("ace") {
		return
	}
	controlhttp.HookMakeACEDialer.Set(mkDialer)
}

func mkDialer(dialer netx.DialFunc, aceHost string, optIP netip.Addr) netx.DialFunc {
	return (&ace.Dialer{
		ACEHost:   aceHost,
		ACEHostIP: optIP, // may be zero
		NetDialer: dialer,
	}).Dial
}
