// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"testing"

	"tappervpn.com/tstest/deptest"
)

func TestDeps(t *testing.T) {
	deptest.DepChecker{
		BadDeps: map[string]string{
			"tappervpn.com/tailcfg": "circular dependency via go generate",
			"tappervpn.com/version": "circular dependency via go generate",
		},
	}.Check(t)
}
