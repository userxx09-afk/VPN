// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows && !linux

package main

import "errors"

const assetName = ""

func install(pkg, tmp string) error {
	return errors.New("tappervpn-updater only installs on Windows and the Linux control server")
}
