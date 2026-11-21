// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"tappervpn.com/control/localcontrol"
	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnauth"
	"tappervpn.com/ipn/ipnstate"
	"tappervpn.com/tailcfg"
)

// Request is one message from the app to the tunnel. Op is one of:
//
//	status                       -> Reply.Model
//	signIn      Server, AuthKey  (no key: Model.AuthURL is where it's approved)
//	signOut
//	setRunning  On               (pause / resume without stopping the tunnel)
//	setExitNode ID               ("" for none)
//	setAllowLAN On
type Request struct {
	Op      string `json:"op"`
	Server  string `json:"server,omitempty"`
	AuthKey string `json:"authKey,omitempty"`
	ID      string `json:"id,omitempty"`
	On      bool   `json:"on,omitempty"`
}

// Reply is the tunnel's answer. Model is set on every successful reply, so
// the app redraws from it after each action.
type Reply struct {
	Error string `json:"error,omitempty"`
	Model *Model `json:"model,omitempty"`
}

// Model is what the app shows. It is the Windows app's Model, less the
// Windows-only parts.
type Model struct {
	State      string   `json:"state"`    // ipn.State: NeedsLogin, Stopped, Starting, Running, ...
	LoggedIn   bool     `json:"loggedIn"` // has a node key
	AuthURL    string   `json:"authURL,omitempty"`
	ControlURL string   `json:"controlURL"`
	DefaultURL string   `json:"defaultURL"`
	Self       *Device  `json:"self,omitempty"`
	Exit       *Device  `json:"exit,omitempty"` // current exit node
	AllowLAN   bool     `json:"allowLAN"`
	Devices    []Device `json:"devices"`
	Health     []string `json:"health,omitempty"`
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DNSName  string `json:"dnsName"`
	IP       string `json:"ip"`
	IPv6     string `json:"ipv6,omitempty"`
	OS       string `json:"os"`
	Online   bool   `json:"online"`
	ExitNode bool   `json:"exitNode"` // offers itself as an exit node
	LastSeen string `json:"lastSeen,omitempty"`
}

func (b *backend) handle(req Request) Reply {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	switch req.Op {
	case "status":
	case "signIn":
		err = b.signIn(ctx, req.Server, req.AuthKey)
	case "signOut":
		err = b.lb.Logout(ctx, ipnauth.Self)
	case "setRunning":
		_, err = b.lb.EditPrefs(&ipn.MaskedPrefs{
			Prefs:          ipn.Prefs{WantRunning: req.On},
			WantRunningSet: true,
		})
	case "setExitNode":
		_, err = b.lb.EditPrefs(&ipn.MaskedPrefs{
			Prefs:         ipn.Prefs{ExitNodeID: tailcfg.StableNodeID(req.ID)},
			ExitNodeIDSet: true,
			ExitNodeIPSet: true, // and clear an exit node set by IP
		})
	case "setAllowLAN":
		_, err = b.lb.EditPrefs(&ipn.MaskedPrefs{
			Prefs:                     ipn.Prefs{ExitNodeAllowLANAccess: req.On},
			ExitNodeAllowLANAccessSet: true,
		})
	default:
		err = fmt.Errorf("unknown request %q", req.Op)
	}
	if err != nil {
		return Reply{Error: err.Error()}
	}
	m := b.model()
	return Reply{Model: &m}
}

// signIn points the backend at server and signs in, like the Windows app:
// with an auth key the device joins by itself; without one, Model.AuthURL
// becomes the page where the sign-in is approved.
func (b *backend) signIn(ctx context.Context, server, authKey string) error {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		server = localcontrol.PublicURL
	}
	if !strings.HasPrefix(server, "https://") && !strings.HasPrefix(server, "http://") {
		return errors.New("the server must start with https:// or http://")
	}
	authKey = strings.TrimSpace(authKey)
	prefs := ipn.NewPrefs()
	prefs.ControlURL = server
	prefs.WantRunning = true
	if err := b.lb.Start(ipn.Options{AuthKey: authKey, UpdatePrefs: prefs}); err != nil {
		return err
	}
	if authKey == "" {
		return b.lb.StartLoginInteractive(ctx)
	}
	return nil
}

func (b *backend) model() Model {
	m := Model{
		DefaultURL: localcontrol.PublicURL,
		Devices:    []Device{},
	}
	st := b.lb.Status()
	m.State = st.BackendState
	m.LoggedIn = st.HaveNodeKey
	m.AuthURL = st.AuthURL
	m.Health = st.Health
	prefs := b.lb.Prefs()
	m.ControlURL = prefs.ControlURL()
	m.AllowLAN = prefs.ExitNodeAllowLANAccess()
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
		m.Exit = &Device{ID: string(exitID), Name: string(exitID)}
	}
	slices.SortFunc(m.Devices, func(a, b Device) int {
		if a.Online != b.Online { // online first, like the other apps
			if a.Online {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return m
}

func toDevice(ps *ipnstate.PeerStatus) Device {
	name, _, _ := strings.Cut(ps.DNSName, ".")
	if name == "" {
		name = ps.HostName
	}
	d := Device{
		ID:       string(ps.ID),
		Name:     name,
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
