// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package tappervpn

import (
	"context"

	"tappervpn.com/client/local"
	"tappervpn.com/client/tappervpn/apitype"
	"tappervpn.com/ipn/ipnstate"
)

// ErrPeerNotFound is an alias for [tappervpn.com/client/local.ErrPeerNotFound].
//
// Deprecated: import [tappervpn.com/client/local] instead.
var ErrPeerNotFound = local.ErrPeerNotFound

// LocalClient is an alias for [tappervpn.com/client/local.Client].
//
// Deprecated: import [tappervpn.com/client/local] instead.
type LocalClient = local.Client

// IPNBusWatcher is an alias for [tappervpn.com/client/local.IPNBusWatcher].
//
// Deprecated: import [tappervpn.com/client/local] instead.
type IPNBusWatcher = local.IPNBusWatcher

// BugReportOpts is an alias for [tappervpn.com/client/local.BugReportOpts].
//
// Deprecated: import [tappervpn.com/client/local] instead.
type BugReportOpts = local.BugReportOpts

// PingOpts is an alias for [tappervpn.com/client/local.PingOpts].
//
// Deprecated: import [tappervpn.com/client/local] instead.
type PingOpts = local.PingOpts

// SetVersionMismatchHandler is an alias for [tappervpn.com/client/local.SetVersionMismatchHandler].
//
// Deprecated: import [tappervpn.com/client/local] instead.
func SetVersionMismatchHandler(f func(clientVer, serverVer string)) {
	local.SetVersionMismatchHandler(f)
}

// IsAccessDeniedError is an alias for [tappervpn.com/client/local.IsAccessDeniedError].
//
// Deprecated: import [tappervpn.com/client/local] instead.
func IsAccessDeniedError(err error) bool {
	return local.IsAccessDeniedError(err)
}

// IsPreconditionsFailedError is an alias for [tappervpn.com/client/local.IsPreconditionsFailedError].
//
// Deprecated: import [tappervpn.com/client/local] instead.
func IsPreconditionsFailedError(err error) bool {
	return local.IsPreconditionsFailedError(err)
}

// WhoIs is an alias for [tappervpn.com/client/local.WhoIs].
//
// Deprecated: import [tappervpn.com/client/local] instead and use [local.Client.WhoIs].
func WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	return local.WhoIs(ctx, remoteAddr)
}

// Status is an alias for [tappervpn.com/client/local.Status].
//
// Deprecated: import [tappervpn.com/client/local] instead.
func Status(ctx context.Context) (*ipnstate.Status, error) {
	return local.Status(ctx)
}

// StatusWithoutPeers is an alias for [tappervpn.com/client/local.StatusWithoutPeers].
//
// Deprecated: import [tappervpn.com/client/local] instead.
func StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	return local.StatusWithoutPeers(ctx)
}
