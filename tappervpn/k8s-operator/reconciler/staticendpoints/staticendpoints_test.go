// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package staticendpoints

import (
	"testing"
)

func TestGetRandomPort(t *testing.T) {
	for range 100 {
		port := getRandomPort()
		if port < tappervpndPortMin || port > tappervpndPortMax {
			t.Errorf("generated port %d which is out of range [%d, %d]", port, tappervpndPortMin, tappervpndPortMax)
		}
	}
}
