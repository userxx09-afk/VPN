// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package sockopts

import (
	"tappervpn.com/types/nettype"
)

// SetICMPErrImmunity is no-op on non-Windows.
func SetICMPErrImmunity(pconn nettype.PacketConn) error {
	return nil
}
