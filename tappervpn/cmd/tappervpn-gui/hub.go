// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/toqueteos/webbrowser"
	"tappervpn.com/client/local"
	"tappervpn.com/control/localcontrol"
	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnstate"
	"tappervpn.com/tailcfg"
	"tappervpn.com/updater"
)

// Model is everything the window and the tray show. It's sent to the
// window as JSON.
type Model struct {
	Version    string   `json:"version"`
	ServiceErr string   `json:"serviceErr,omitempty"` // service unreachable
	State      string   `json:"state"`                // ipn.State: NeedsLogin, Stopped, Starting, Running, ...
	LoggedIn   bool     `json:"loggedIn"`             // has a node key (signed in, even if stopped)
	AuthURL    string   `json:"authURL,omitempty"`    // approve this PC here
	ControlURL string   `json:"controlURL"`
	DefaultURL string   `json:"defaultURL"`
	AdminURL   string   `json:"adminURL"`
	Self       *Device  `json:"self,omitempty"`
	Exit       *Device  `json:"exit,omitempty"` // current exit node
	AllowLAN   bool     `json:"allowLAN"`
	Devices    []Device `json:"devices"`
	Health     []string `json:"health,omitempty"`
	Error      string   `json:"error,omitempty"` // last failed action
	Busy       string   `json:"busy,omitempty"`  // action in progress
	Update     Update   `json:"update"`
}

type Device struct {
	ID       tailcfg.StableNodeID `json:"id"`
	Name     string               `json:"name"`
	DNSName  string               `json:"dnsName"`
	IP       string               `json:"ip"`
	IPv6     string               `json:"ipv6,omitempty"`
	OS       string               `json:"os"`
	Online   bool                 `json:"online"`
	ExitNode bool                 `json:"exitNode"` // offers itself as an exit node
	LastSeen string               `json:"lastSeen,omitempty"`
}

type Update struct {
	Checked   bool   `json:"checked"`
	Checking  bool   `json:"checking"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Err       string `json:"err,omitempty"`
	Started   bool   `json:"started"` // install task started
}

// hub reads the service's state, keeps the latest Model, and tells the
// tray and open windows when it changes.
type hub struct {
	lc *local.Client

	kickCh chan struct{}

	mu        sync.Mutex
	model     Model
	authURL   string
	signingIn bool // "Sign in" clicked, approval page not opened yet
	lastErr   string
	busy      string
	upd       Update
	subs      map[chan Model]struct{}
}

func newHub(lc *local.Client) *hub {
	return &hub{
		lc:     lc,
		kickCh: make(chan struct{}, 1),
		subs:   map[chan Model]struct{}{},
	}
}

func (h *hub) kick() {
	select {
	case h.kickCh <- struct{}{}:
	default:
	}
}

func (h *hub) run() {
	go h.watchBus()
	go func() {
		h.checkUpdate()
		for range time.Tick(6 * time.Hour) {
			h.checkUpdate()
		}
	}()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	h.refresh()
	for {
		select {
		case <-h.kickCh:
		case <-tick.C:
		}
		time.Sleep(250 * time.Millisecond) // let a burst of changes settle
		select {
		case <-h.kickCh:
		default:
		}
		h.refresh()
	}
}

// watchBus follows the service's change notifications, reconnecting when
// the service restarts.
func (h *hub) watchBus() {
	for {
		w, err := h.lc.WatchIPNBus(context.Background(), ipn.NotifyInitialState)
		if err == nil {
			for {
				n, err := w.Next()
				if err != nil {
					break
				}
				if n.BrowseToURL != nil && *n.BrowseToURL != "" {
					// Open the approval page right after "Sign in" is clicked;
					// otherwise the window just shows a button for it.
					h.mu.Lock()
					open := h.signingIn && h.authURL != *n.BrowseToURL
					h.authURL = *n.BrowseToURL
					h.signingIn = false
					h.mu.Unlock()
					if open {
						webbrowser.Open(*n.BrowseToURL)
					}
				}
				if n.State != nil && *n.State == ipn.Running {
					h.mu.Lock()
					h.authURL, h.signingIn = "", false
					h.mu.Unlock()
				}
				if n.State != nil || n.Prefs != nil || n.SelfChange != nil || len(n.PeersChanged) > 0 || len(n.PeerChangedPatch) > 0 || len(n.PeersRemoved) > 0 || n.LoginFinished != nil || n.BrowseToURL != nil || n.ErrMessage != nil {
					if n.ErrMessage != nil && *n.ErrMessage != "" {
						h.setErr(*n.ErrMessage)
					}
					h.kick()
				}
			}
			w.Close()
		}
		h.kick() // show that the service went away
		time.Sleep(3 * time.Second)
	}
}

func (h *hub) Subscribe() (chan Model, func()) {
	ch := make(chan Model, 4)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	m := h.model
	h.mu.Unlock()
	ch <- m
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) Model() Model {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.model
}

func shortName(ps *ipnstate.PeerStatus) string {
	if n, _, _ := strings.Cut(ps.DNSName, "."); n != "" {
		return n
	}
	return ps.HostName
}

func toDevice(ps *ipnstate.PeerStatus) Device {
	d := Device{
		ID:       ps.ID,
		Name:     shortName(ps),
		DNSName:  strings.TrimSuffix(ps.DNSName, "."),
		OS:       ps.OS,
		Online:   ps.Online,
		ExitNode: ps.ExitNodeOption,
	}
	for _, ip := range ps.TapperVPNIPs {
		if ip.Is4() && d.IP == "" {
			d.IP = ip.String()
		} else if ip.Is6() && d.IPv6 == "" {
			d.IPv6 = ip.String()
		}
	}
	if !ps.Online && !ps.LastSeen.IsZero() {
		d.LastSeen = ago(ps.LastSeen)
	}
	return d
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (h *hub) read() Model {
	m := Model{
		Version:    updater.Version,
		DefaultURL: localcontrol.PublicURL,
		AdminURL:   *adminURL,
		Devices:    []Device{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := h.lc.Status(ctx)
	if err != nil {
		m.ServiceErr = "The TapperVPN service isn't running. Restart the PC, or run TapperVPN setup again."
		return m
	}
	m.State = st.BackendState
	m.LoggedIn = st.HaveNodeKey
	if st.AuthURL != "" {
		m.AuthURL = st.AuthURL
	}
	m.Health = st.Health
	if prefs, err := h.lc.GetPrefs(ctx); err == nil {
		m.ControlURL = prefs.ControlURL
		m.AllowLAN = prefs.ExitNodeAllowLANAccess
	}
	if st.Self != nil && len(st.Self.TapperVPNIPs) > 0 {
		d := toDevice(st.Self)
		d.Online = st.BackendState == ipn.Running.String()
		m.Self = &d
	}
	var exitID tailcfg.StableNodeID
	if es := st.ExitNodeStatus; es != nil && !es.ID.IsZero() {
		exitID = es.ID
	}
	for _, ps := range st.Peer {
		d := toDevice(ps)
		m.Devices = append(m.Devices, d)
		if ps.ID == exitID {
			e := d
			e.Online = st.ExitNodeStatus.Online
			m.Exit = &e
		}
	}
	if exitID != "" && m.Exit == nil {
		m.Exit = &Device{ID: exitID, Name: string(exitID)}
	}
	slices.SortFunc(m.Devices, func(a, b Device) int {
		if a.Online != b.Online { // online first, like the Android app
			if a.Online {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return m
}

func (h *hub) refresh() {
	m := h.read()
	h.mu.Lock()
	if m.AuthURL == "" && m.State != ipn.Running.String() {
		m.AuthURL = h.authURL
	}
	m.Error, m.Busy, m.Update = h.lastErr, h.busy, h.upd
	same := modelKey(m) == modelKey(h.model)
	h.model = m
	subs := make([]chan Model, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	if same {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- m:
		default: // slow window; it gets the next one
		}
	}
	trayUpdate(m)
}

func modelKey(m Model) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func (h *hub) setErr(msg string) {
	h.mu.Lock()
	h.lastErr = msg
	h.mu.Unlock()
}

// do runs one change against the service, showing it as busy meanwhile
// and its error, if any, until the next action.
func (h *hub) do(what string, fn func(context.Context) error) error {
	h.mu.Lock()
	h.busy, h.lastErr = what, ""
	h.mu.Unlock()
	h.kick()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := fn(ctx)
	h.mu.Lock()
	h.busy = ""
	if err != nil {
		log.Printf("%s: %v", what, err)
		msg := fmt.Sprintf("%s failed: %v", what, err)
		if local.IsAccessDeniedError(err) {
			msg = what + " failed: this Windows user isn't allowed to change TapperVPN. Ask the PC's administrator."
		}
		h.lastErr = msg
	}
	h.mu.Unlock()
	h.kick()
	return err
}

func (h *hub) SetRunning(on bool) error {
	what := "Disconnecting"
	if on {
		what = "Connecting"
	}
	return h.do(what, func(ctx context.Context) error {
		_, err := h.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
			Prefs:          ipn.Prefs{WantRunning: on},
			WantRunningSet: true,
		})
		return err
	})
}

func (h *hub) SetExitNode(id tailcfg.StableNodeID) error {
	return h.do("Changing the exit node", func(ctx context.Context) error {
		_, err := h.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
			Prefs:         ipn.Prefs{ExitNodeID: id},
			ExitNodeIDSet: true,
			ExitNodeIPSet: true, // and clear an exit node set by IP
		})
		return err
	})
}

func (h *hub) SetAllowLAN(on bool) error {
	return h.do("Changing LAN access", func(ctx context.Context) error {
		_, err := h.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
			Prefs:                     ipn.Prefs{ExitNodeAllowLANAccess: on},
			ExitNodeAllowLANAccessSet: true,
		})
		return err
	})
}

// SignIn points the service at server and signs in: with authKey the PC
// joins by itself; without one the service gives a URL where the sign-in
// is approved (shown in the window and opened in the browser). Like
// `tappervpn up --unattended`, the VPN then stays up while nobody is
// signed in to Windows.
func (h *hub) SignIn(server, authKey string) error {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		server = localcontrol.PublicURL
	}
	if !strings.HasPrefix(server, "https://") && !strings.HasPrefix(server, "http://") {
		err := errors.New("the server must start with https:// or http://")
		h.setErr("Signing in failed: " + err.Error())
		h.kick()
		return err
	}
	h.mu.Lock()
	h.authURL, h.signingIn = "", authKey == ""
	h.mu.Unlock()
	return h.do("Signing in", func(ctx context.Context) error {
		prefs := ipn.NewPrefs()
		prefs.ControlURL = server
		prefs.WantRunning = true
		prefs.ForceDaemon = true
		if err := h.lc.Start(ctx, ipn.Options{AuthKey: strings.TrimSpace(authKey), UpdatePrefs: prefs}); err != nil {
			return err
		}
		if authKey == "" {
			return h.lc.StartLoginInteractive(ctx)
		}
		return nil
	})
}

func (h *hub) SignOut() error {
	return h.do("Signing out", func(ctx context.Context) error { return h.lc.Logout(ctx) })
}

func (h *hub) checkUpdate() {
	h.mu.Lock()
	if h.upd.Checking {
		h.mu.Unlock()
		return
	}
	h.upd.Checking = true
	h.mu.Unlock()
	h.kick()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rel, err := updater.Latest(ctx)

	h.mu.Lock()
	h.upd.Checking, h.upd.Checked = false, true
	if err != nil {
		h.upd.Err = err.Error()
	} else {
		h.upd.Err = ""
		h.upd.Latest = rel.Version
		h.upd.Available = updater.IsNewer(rel.Version, updater.Version)
	}
	h.mu.Unlock()
	h.kick()
}

// InstallUpdate runs the "TapperVPN Update" scheduled task, which installs
// the signed update as SYSTEM (setup lets every user start that task).
// The service restarts, and this app restarts when its exe is replaced.
func (h *hub) InstallUpdate() error {
	return h.do("Installing the update", func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "schtasks.exe", "/run", "/tn", "TapperVPN Update")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if strings.Contains(strings.ToLower(msg), "denied") {
				return errors.New("Windows didn't allow starting the update. Run TapperVPN setup again (it fixes this), or wait for the nightly update")
			}
			return fmt.Errorf("%v: %s", err, msg)
		}
		h.mu.Lock()
		h.upd.Started = true
		h.mu.Unlock()
		return nil
	})
}
