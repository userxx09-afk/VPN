// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ts_omit_debug && ts_omit_connreject

package wgengine

import (
	"tappervpn.com/net/flowtrack"
	"tappervpn.com/net/packet"
)

// notifyConnRejectTSMPRecv is a no-op when the connreject feature is
// omitted at build time.
func (*userspaceEngine) notifyConnRejectTSMPRecv(packet.TapperVPNRejectedHeader) {}

// notifyConnRejectOpenTimeout is a no-op when the connreject feature is
// omitted at build time.
func (*userspaceEngine) notifyConnRejectOpenTimeout(flowtrack.Tuple, openTimeoutDiag) {}
