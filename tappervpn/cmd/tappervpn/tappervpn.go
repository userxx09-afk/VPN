// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// The tappervpn command is the TapperVPN command-line client. It interacts
// with the tappervpnd node agent.
package main // import "tappervpn.com/cmd/tappervpn"

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tappervpn.com/cmd/tappervpn/cli"
)

func main() {
	args := os.Args[1:]
	if name, _ := os.Executable(); strings.HasSuffix(filepath.Base(name), ".cgi") {
		args = []string{"web", "-cgi"}
	}
	if err := cli.Run(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
