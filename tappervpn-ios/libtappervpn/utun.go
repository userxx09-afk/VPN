// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// From <sys/kern_control.h> and <net/if_utun.h>; x/sys/unix lacks them.
const (
	sysprotoControl = 2
	utunOptIfname   = 2
)

// findTunFD finds the packet tunnel's utun socket among the process's open
// files. NEPacketTunnelProvider doesn't hand it out, but it is an open
// kernel-control socket in this process once the first network settings are
// applied (WireGuard's iOS app finds it the same way).
func findTunFD() (int, string, error) {
	for fd := 0; fd < 1024; fd++ {
		name, err := unix.GetsockoptString(fd, sysprotoControl, utunOptIfname)
		if err == nil && strings.HasPrefix(name, "utun") {
			return fd, name, nil
		}
	}
	return -1, "", errors.New("the tunnel interface wasn't found; apply network settings before Start")
}
