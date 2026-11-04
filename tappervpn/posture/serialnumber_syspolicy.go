// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build android || ios

package posture

import (
	"fmt"

	"tappervpn.com/types/logger"
	"tappervpn.com/util/syspolicy/pkey"
	"tappervpn.com/util/syspolicy/policyclient"
)

// GetSerialNumbers returns the serial number of the device as reported by an
// MDM solution. It requires configuration via the DeviceSerialNumber system policy.
// This is the only way to gather serial numbers on iOS, tvOS and Android.
func GetSerialNumbers(polc policyclient.Client, _ logger.Logf) ([]string, error) {
	s, err := polc.GetString(pkey.DeviceSerialNumber, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get serial number from MDM: %v", err)
	}
	if s != "" {
		return []string{s}, nil
	}
	return nil, nil
}
