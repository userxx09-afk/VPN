// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// singleInstance makes this the only helper in the sign-in session: the
// TapperVPN app starts the helper whenever this mutex is missing, and a logon
// task may start it too. It returns false if another helper already runs.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\TapperVPNRemoteHelper`)
	_, err := windows.CreateMutex(nil, false, name) // held until exit
	return err != windows.ERROR_ALREADY_EXISTS
}

// setupLog writes to %LOCALAPPDATA%\TapperVPN\remote.log; the helper has no
// console, and this log is how a failed session gets diagnosed.
func setupLog() {
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	dir = filepath.Join(dir, "TapperVPN")
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, "remote.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
}
