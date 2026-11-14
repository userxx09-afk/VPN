// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// singleInstance makes this the only helper in the sign-in session: the
// TapperVPN app starts the helper whenever this mutex is missing, and a logon
// task may start it too. It returns false if another helper already runs.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\TapperVPNRemoteHelper`)
	_, err := windows.CreateMutex(nil, false, name) // held until exit
	if err == windows.ERROR_ALREADY_EXISTS {
		return false
	}
	stopOldHelpers()
	return true
}

// stopOldHelpers ends other tappervpn-remote.exe processes in this session.
// Helpers from before 0.2.19 don't hold the mutex, so without this an old one
// (from a hand install, or one an older updater didn't stop) keeps running
// beside this one and can end up serving the user's Control window.
func stopOldHelpers() {
	self := windows.GetCurrentProcessId()
	var mySession uint32
	if windows.ProcessIdToSessionId(self, &mySession) != nil {
		return
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == self || !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "tappervpn-remote.exe") {
			continue
		}
		var session uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &session) != nil || session != mySession {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pe.ProcessID)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(h, 1) == nil {
			log.Printf("stopped an older helper (pid %d)", pe.ProcessID)
		}
		windows.CloseHandle(h)
	}
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
