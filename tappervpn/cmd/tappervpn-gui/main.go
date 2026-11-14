// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

// tappervpn-gui is the TapperVPN app for Windows: the icon in the taskbar's
// notification area and the TapperVPN window, the same things the Android
// app does. It drives the TapperVPN service (tappervpnd) through the
// LocalAPI, like tappervpn.exe does.
//
//   - Sign in: pick the server (https://vp.tapper.co) and optionally an auth key
//   - Connect / Disconnect, exit node (or None), LAN access
//   - This PC's VPN address and every other device's, with copy buttons
//   - Open the admin page, check for and install updates, sign out
//
// One process per Windows sign-in. The installer starts it at sign-in with
// -background (icon only); starting it again (Start menu) opens the window.
// When the updater swaps in a new exe, the running copy restarts from it.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"tappervpn.com/client/local"
	"tappervpn.com/updater"
)

var (
	background = flag.Bool("background", false, "start with only the taskbar icon, without opening the window")
	adminURL   = flag.String("admin-url", "http://100.64.0.2:8096", "the VPN admin page")
)

// instance is written to %LOCALAPPDATA%\TapperVPN\gui.json so a second
// start can ask the running copy to open its window.
type instance struct {
	URL string // http://127.0.0.1:port/t/<token>/
	PID int
}

func dataDir() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "TapperVPN")
	os.MkdirAll(dir, 0o700)
	return dir
}

func main() {
	flag.Parse()
	setupLog()

	if os.Getenv("TAPPERVPN_GUI_RESTART") != "" {
		// Started by an old copy that is exiting; let it release the mutex.
		os.Unsetenv("TAPPERVPN_GUI_RESTART")
		time.Sleep(2 * time.Second)
	}

	name, _ := windows.UTF16PtrFromString(`Local\TapperVPNApp`)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		// Already running: open its window.
		if !*background {
			if b, err := os.ReadFile(filepath.Join(dataDir(), "gui.json")); err == nil {
				var in instance
				if json.Unmarshal(b, &in) == nil && in.URL != "" {
					openWindow(in.URL)
				}
			}
		}
		return
	}

	h := newHub(&local.Client{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	tok := make([]byte, 24)
	rand.Read(tok)
	srv := &webServer{hub: h, token: hex.EncodeToString(tok), addr: ln.Addr().String()}
	go http.Serve(ln, srv)
	appURL := "http://" + srv.addr + "/t/" + srv.token + "/"

	b, _ := json.Marshal(instance{URL: appURL, PID: os.Getpid()})
	os.WriteFile(filepath.Join(dataDir(), "gui.json"), b, 0o600)

	t := &tray{hub: h, open: func() { openWindow(appURL) }}
	go h.run()
	go watchUpdate()
	go superviseHelper()
	if !*background {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openWindow(appURL)
		}()
	}
	systray.Run(t.onReady, func() {})
}

// setupLog writes to %LOCALAPPDATA%\TapperVPN\gui.log (there's no console).
func setupLog() {
	p := filepath.Join(dataDir(), "gui.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.Printf("tappervpn-gui %s starting", updater.Version)
}

// watchUpdate restarts the app when its exe is replaced (the updater renames
// the running file to .old and writes the new one in its place).
func watchUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	start, err := os.Stat(exe)
	if err != nil {
		return
	}
	same := func(a, b os.FileInfo) bool { return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) }
	for range time.Tick(20 * time.Second) {
		fi, err := os.Stat(exe)
		if err != nil || same(fi, start) {
			continue
		}
		time.Sleep(10 * time.Second) // until the new file stops changing
		if fi2, err := os.Stat(exe); err != nil || !same(fi, fi2) {
			continue
		}
		log.Printf("%s was updated; restarting", exe)
		cmd := exec.Command(exe, "-background")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Env = append(os.Environ(), "TAPPERVPN_GUI_RESTART=1")
		if err := cmd.Start(); err != nil {
			log.Printf("restart: %v", err)
			continue
		}
		systray.Quit()
		os.Exit(0)
	}
}
