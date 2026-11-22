// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/toqueteos/webbrowser"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const windowTitle = "TapperVPN"

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procFindWindow    = user32.NewProc("FindWindowW")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procShowWindow    = user32.NewProc("ShowWindow")
	procIsIconic      = user32.NewProc("IsIconic")
	procPostMessage   = user32.NewProc("PostMessageW")
	chromeWindowClass = windows.StringToUTF16Ptr("Chrome_WidgetWin_1")
	windowTitleUTF16  = windows.StringToUTF16Ptr(windowTitle)
	swRestore         = uintptr(9)
	wmClose           = uintptr(0x0010)
)

// closeWindow closes the TapperVPN window if it's open, so quitting from the
// tray doesn't leave a window behind that can no longer do anything.
func closeWindow() {
	for i := 0; i < 5; i++ { // more than one may be open
		hwnd, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(chromeWindowClass)), uintptr(unsafe.Pointer(windowTitleUTF16)))
		if hwnd == 0 {
			return
		}
		procPostMessage.Call(hwnd, wmClose, 0, 0)
		time.Sleep(200 * time.Millisecond)
	}
}

// openWindow shows the TapperVPN window: brings it to the front if it's
// open, else opens the page as an Edge app window (no tabs or address bar),
// or in the default browser if Edge is missing.
func openWindow(url string) {
	if hwnd, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(chromeWindowClass)), uintptr(unsafe.Pointer(windowTitleUTF16))); hwnd != 0 {
		if min, _, _ := procIsIconic.Call(hwnd); min != 0 {
			procShowWindow.Call(hwnd, swRestore)
		}
		procSetForeground.Call(hwnd)
		return
	}
	if edge := edgePath(); edge != "" {
		profile := filepath.Join(dataDir(), "window")
		cmd := exec.Command(edge,
			"--app="+url,
			"--user-data-dir="+profile,
			"--window-size=460,820",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate",
		)
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return
		} else {
			log.Printf("starting Edge: %v", err)
		}
	}
	if err := webbrowser.Open(url); err != nil {
		log.Printf("opening %s: %v", url, err)
	}
}

func edgePath() string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("")
		k.Close()
		if err == nil && fileExists(p) {
			return p
		}
	}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		p := filepath.Join(os.Getenv(env), `Microsoft\Edge\Application\msedge.exe`)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
