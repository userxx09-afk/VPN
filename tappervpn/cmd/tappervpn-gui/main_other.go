// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

// tappervpn-gui is the TapperVPN app for Windows. On Linux, use cmd/systray.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tappervpn-gui is the Windows app; on Linux use cmd/systray")
	os.Exit(1)
}
