// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// supervised reports whether this is the service's child process, which the
// service's parent process restarts when it exits (see babysitProc in
// cmd/tappervpnd).
func supervised() bool { return len(os.Args) == 3 && os.Args[1] == "/subproc" }

func exitForRestart() { os.Exit(1) }

// The service runs as SYSTEM, which may reboot the PC.
func canRebootMachine() bool { return supervised() }

func rebootMachine(from string) error {
	msg := fmt.Sprintf("%s asked TapperVPN to restart this PC. It restarts in 30 seconds; save your work.", from)
	if len(msg) > 500 {
		msg = msg[:500]
	}
	cmd := exec.Command("shutdown.exe", "/r", "/t", "30", "/d", "p:0:0", "/c", msg)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("shutdown.exe: %v: %s", err, out)
	}
	return nil
}
