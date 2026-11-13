// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package main

import "errors"

// newHostPlatform reports that this OS has no host implementation yet. The
// helper currently targets Windows; other platforms host via their own in-app
// paths (e.g. Android) and do not use this helper.
func newHostPlatform() (Platform, error) {
	return nil, errors.New("the remote-control helper is only supported on Windows")
}
