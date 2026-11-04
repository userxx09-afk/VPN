// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ts_omit_webclient

package main

import (
	"tappervpn.com/client/local"
	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/paths"
)

func init() {
	hookConfigureWebClient.Set(func(lb *ipnlocal.LocalBackend) {
		lb.ConfigureWebClient(&local.Client{
			Socket:        args.socketpath,
			UseSocketOnly: args.socketpath != paths.DefaultTapperVPNdSocket(),
		})
	})
}
