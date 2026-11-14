// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// superviseHelper keeps the remote-control helper (tappervpn-remote.exe)
// running in this sign-in session. The helper does the desktop work the
// service can't (screen capture, input, consent prompts, the viewer window).
// It holds the Local\TapperVPNRemoteHelper mutex while running, so this only
// starts it when it isn't. The updater stops the helper after replacing it,
// and this starts the new one within seconds.
func superviseHelper() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	helper := filepath.Join(filepath.Dir(exe), "tappervpn-remote.exe")
	name, _ := windows.UTF16PtrFromString(`Local\TapperVPNRemoteHelper`)
	for {
		if _, err := os.Stat(helper); err == nil {
			h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
			if err == nil {
				windows.CloseHandle(h) // running
			} else {
				cmd := exec.Command(helper)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if err := cmd.Start(); err != nil {
					log.Printf("starting remote helper: %v", err)
				} else {
					log.Printf("started remote helper (pid %d)", cmd.Process.Pid)
					go cmd.Wait()
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
}
