// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"context"
	"fmt"
	"sync"

	"fyne.io/systray"
	"github.com/atotto/clipboard"
	"github.com/toqueteos/webbrowser"
	"tappervpn.com/ipn"
	"tappervpn.com/tailcfg"
)

// tray is the notification-area icon. Left click opens the window; right
// click shows quick controls.
type tray struct {
	hub  *hub
	open func()
}

var (
	theTray   *tray
	trayMu    sync.Mutex
	trayReady bool
	trayShown string // menu fingerprint on screen
	trayStop  context.CancelFunc
)

func (t *tray) onReady() {
	theTray = t
	systray.SetTitle("TapperVPN")
	systray.SetIcon(iconICO(stateOff))
	systray.SetTooltip("TapperVPN")
	systray.SetOnTapped(t.open)
	trayMu.Lock()
	trayReady = true
	trayMu.Unlock()
	trayUpdate(t.hub.Model())
}

func trayUpdate(m Model) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayReady || theTray == nil {
		return
	}
	running := m.State == ipn.Running.String()
	icon, tip := stateOff, "TapperVPN: disconnected"
	switch {
	case m.ServiceErr != "":
		tip = "TapperVPN: service isn't running"
	case running && m.Exit != nil && m.Exit.Online:
		icon, tip = stateExit, "TapperVPN: connected via "+m.Exit.Name
	case running && m.Exit != nil:
		icon, tip = stateWait, "TapperVPN: exit node "+m.Exit.Name+" is offline"
	case running:
		icon, tip = stateOn, "TapperVPN: connected"
	case m.State == ipn.Starting.String():
		icon, tip = stateWait, "TapperVPN: connecting..."
	case m.State == ipn.NeedsLogin.String() || m.State == ipn.NoState.String():
		tip = "TapperVPN: not signed in"
	}
	if running && m.Self != nil {
		tip += "\n" + m.Self.IP
	}
	systray.SetIcon(iconICO(icon))
	systray.SetTooltip(tip)

	// Rebuild the menu only when something on it changed.
	key := fmt.Sprintf("%s|%v|%v|%v", m.State, m.ServiceErr != "", m.Self, m.Exit)
	for _, d := range m.Devices {
		if d.ExitNode {
			key += fmt.Sprintf("|%s:%v", d.ID, d.Online)
		}
	}
	if key == trayShown {
		return
	}
	trayShown = key
	if trayStop != nil {
		trayStop()
	}
	var ctx context.Context
	ctx, trayStop = context.WithCancel(context.Background())
	theTray.build(ctx, m)
}

func (t *tray) build(ctx context.Context, m Model) {
	h := t.hub
	systray.ResetMenu()
	running := m.State == ipn.Running.String()
	signedOut := m.ServiceErr == "" && (m.State == ipn.NeedsLogin.String() || (m.State == ipn.NoState.String() && !m.LoggedIn))

	open := systray.AddMenuItem("Open TapperVPN", "")
	onClick(ctx, open, t.open)
	systray.AddSeparator()

	status := "Disconnected"
	switch {
	case m.ServiceErr != "":
		status = "Service isn't running"
	case running:
		status = "Connected"
	case m.State == ipn.Starting.String():
		status = "Connecting..."
	case signedOut:
		status = "Not signed in"
	}
	systray.AddMenuItem(status, "").Disable()
	if m.Self != nil {
		self := systray.AddMenuItem(fmt.Sprintf("This PC: %s", m.Self.IP), "Click to copy")
		ip := m.Self.IP
		onClick(ctx, self, func() { clipboard.WriteAll(ip) })
	}
	systray.AddSeparator()

	switch {
	case m.ServiceErr != "":
	case signedOut:
		in := systray.AddMenuItem("Sign in...", "")
		onClick(ctx, in, t.open)
	default:
		if running || m.State == ipn.Starting.String() {
			it := systray.AddMenuItem("Disconnect", "")
			onClick(ctx, it, func() { h.SetRunning(false) })
		} else {
			it := systray.AddMenuItem("Connect", "")
			onClick(ctx, it, func() { h.SetRunning(true) })
		}
		title := "Exit node: None"
		if m.Exit != nil {
			title = "Exit node: " + m.Exit.Name
		}
		exits := systray.AddMenuItem(title, "")
		none := exits.AddSubMenuItemCheckbox("None", "", m.Exit == nil)
		onClick(ctx, none, func() { h.SetExitNode("") })
		any := false
		for _, d := range m.Devices {
			if !d.ExitNode {
				continue
			}
			any = true
			name := d.Name
			if !d.Online {
				name += " (offline)"
			}
			cur := m.Exit != nil && m.Exit.ID == d.ID
			it := exits.AddSubMenuItemCheckbox(name, d.IP, cur)
			if !d.Online && !cur {
				it.Disable()
			}
			id := d.ID
			onClick(ctx, it, func() { h.SetExitNode(tailcfg.StableNodeID(id)) })
		}
		if !any {
			exits.AddSubMenuItem("No device offers an exit node", "").Disable()
		}
	}
	admin := systray.AddMenuItem("Admin page", m.AdminURL)
	url := m.AdminURL
	onClick(ctx, admin, func() { webbrowser.Open(url) })
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Close the app (the VPN keeps running)")
	onClick(ctx, quit, systray.Quit)
}

func onClick(ctx context.Context, it *systray.MenuItem, fn func()) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-it.ClickedCh:
				go fn()
			}
		}
	}()
}
