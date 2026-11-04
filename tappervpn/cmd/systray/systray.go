// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build cgo || !darwin

// systray is a minimal TapperVPN systray application.
package main

import (
	"flag"

	"tappervpn.com/client/local"
	"tappervpn.com/client/systray"
	"tappervpn.com/paths"
)

var socket = flag.String("socket", paths.DefaultTapperVPNdSocket(), "path to tappervpnd socket")
var theme = flag.String("theme", "dark", "color theme for TapperVPN icon: dark, dark:nobg, light, light:nobg")

func main() {
	flag.Parse()
	lc := &local.Client{Socket: *socket}
	systray.SetTheme(*theme)
	new(systray.Menu).Run(lc)
}
