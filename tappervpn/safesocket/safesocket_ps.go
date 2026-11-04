// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build ((linux && !android) || windows || (darwin && !ios) || freebsd) && !ts_omit_cliconndiag

package safesocket

import (
	"strings"

	ps "github.com/mitchellh/go-ps"
)

func init() {
	tappervpndProcExists.Set(func() bool {
		procs, err := ps.Processes()
		if err != nil {
			return false
		}
		for _, proc := range procs {
			name := proc.Executable()
			const tappervpnd = "tappervpnd"
			if len(name) < len(tappervpnd) {
				continue
			}
			// Do case insensitive comparison for Windows,
			// notably, and ignore any ".exe" suffix.
			if strings.EqualFold(name[:len(tappervpnd)], tappervpnd) {
				return true
			}
		}
		return false
	})
}
