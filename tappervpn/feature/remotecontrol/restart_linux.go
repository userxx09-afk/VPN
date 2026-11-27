// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux && !android

package remotecontrol

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// supervised reports whether something will start us again: the router's
// procd script sets TAPPERVPN_RESTART_CMD (its restart also re-applies
// routing), and systemd sets INVOCATION_ID for services (tappervpnd.service
// has Restart=on-failure).
func supervised() bool {
	return os.Getenv("TAPPERVPN_RESTART_CMD") != "" || os.Getenv("INVOCATION_ID") != ""
}

// exitForRestart runs TAPPERVPN_RESTART_CMD in its own session (so it outlives
// us), or else exits non-zero so Restart=on-failure starts us again.
func exitForRestart() {
	if c := os.Getenv("TAPPERVPN_RESTART_CMD"); c != "" {
		cmd := exec.Command("/bin/sh", "-c", c)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err == nil {
			return
		}
	}
	os.Exit(1)
}

func canRebootMachine() bool { return os.Geteuid() == 0 }

func rebootMachine(from string) error {
	time.Sleep(28 * time.Second) // match the "about 30 seconds" we told the requester
	for _, c := range [][]string{{"systemctl", "reboot"}, {"reboot"}, {"/sbin/reboot"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", c[0], err, out)
		}
		return nil
	}
	return fmt.Errorf("no reboot command found")
}
