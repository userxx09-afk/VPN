// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build ts_omit_connreject

package tstun

import "tappervpn.com/net/packet"

// connRejectState is the omit-build counterpart of the type in
// connreject.go. It is an empty struct so the Wrapper's connReject
// field exists in both builds without referencing
// tappervpn.com/net/connreject.
type connRejectState struct{}

// notifyConnRejectTSMPSent is a no-op when the connreject feature is
// omitted at build time.
func (*Wrapper) notifyConnRejectTSMPSent(packet.TapperVPNRejectedHeader) {}
