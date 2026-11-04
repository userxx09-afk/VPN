// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package approuting

// newOSOwnerLookup returns nil: per-flow owner lookup is only implemented
// on Windows. Other OSes select apps at the VPN API level instead.
func newOSOwnerLookup() *ownerLookup { return nil }
