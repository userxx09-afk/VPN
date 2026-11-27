// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/localapi"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
)

// Remote restart lets one device ask another to restart its VPN (or, where the
// platform allows, the whole device) when it's having problems.
//
// Only devices an admin switched on with "Can restart others" on the Tapper
// Dashboard may send one: that gives them RestarterTag. The receiving device
// checks the tag itself, from the control server's signed network map, so a
// device can't claim it. There is no prompt on the receiving side (the point is
// to fix a device nobody is sitting at), but it says who restarted it.
const RestarterTag = "tag:remote-restart"

// RestartKind is what to restart.
type RestartKind string

const (
	// RestartVPN restarts the TapperVPN service or app (the tunnel and its
	// connection to the server). Every platform supports it.
	RestartVPN RestartKind = "vpn"
	// RestartDevice reboots the whole device. Only devices whose VPN runs as a
	// system service can (Windows, Linux, routers); phone and TV apps can't.
	RestartDevice RestartKind = "device"
)

// Restarter carries out a restart on this device. The default (see
// restart_platform.go) suits the daemon on Windows, Linux and routers;
// Android installs its own with RegisterRestarter.
type Restarter interface {
	// CanRestart reports whether this device can do kind.
	CanRestart(kind RestartKind) bool
	// Restart does it. from is the requesting device's name, for messages and
	// logs. It is called after the reply has been sent, from its own goroutine.
	Restart(kind RestartKind, from string) error
}

var restarter struct {
	mu sync.Mutex
	r  Restarter
}

// RegisterRestarter replaces this process's Restarter.
func RegisterRestarter(r Restarter) {
	restarter.mu.Lock()
	defer restarter.mu.Unlock()
	restarter.r = r
}

func getRestarter(b *ipnlocal.LocalBackend, logf logger.Logf) Restarter {
	restarter.mu.Lock()
	defer restarter.mu.Unlock()
	if restarter.r != nil {
		return restarter.r
	}
	return &defaultRestarter{b: b, logf: logf}
}

// isRestarter reports whether n was allowed on the dashboard to restart others.
func isRestarter(n tailcfg.NodeView) bool {
	if !n.Valid() {
		return false
	}
	for _, t := range n.Tags().All() {
		if t == RestarterTag {
			return true
		}
	}
	return false
}

// osCanReboot reports whether a device with this Hostinfo.OS runs its VPN as a
// system service that can reboot the machine.
func osCanReboot(os string) bool {
	switch strings.ToLower(os) {
	case "windows", "linux":
		return true
	}
	return false
}

func parseRestartKind(s string) (RestartKind, bool) {
	switch k := RestartKind(s); k {
	case RestartVPN, RestartDevice:
		return k, true
	case "":
		return RestartVPN, true
	}
	return "", false
}

// RestartReply is the JSON reply to a restart request.
type RestartReply struct {
	OK      bool        `json:"ok"`
	What    RestartKind `json:"what"`
	Message string      `json:"message"`
}

// restartDelay gives the reply time to reach the requester before the
// connection it travels on goes away.
const restartDelay = 2 * time.Second

// handleRestart is the receiving side: POST /remotecontrol/v0/restart?what=vpn|device
// over PeerAPI, so h.Peer() is the authenticated requesting device.
func handleRestart(h ipnlocal.PeerAPIHandler, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	peer := h.Peer()
	from := peerDisplayName(peer)
	self := peerDisplayName(h.Self())
	if !isRestarter(peer) {
		h.Logf("remotecontrol: refused restart request from %q: not allowed on the dashboard", from)
		http.Error(w, fmt.Sprintf("%s isn't allowed to restart other devices. An admin can allow it with \"Can restart others\" on the Tapper Dashboard.", from), http.StatusForbidden)
		return
	}
	kind, ok := parseRestartKind(r.URL.Query().Get("what"))
	if !ok {
		http.Error(w, "what must be vpn or device", http.StatusBadRequest)
		return
	}
	rs := getRestarter(h.LocalBackend(), h.Logf)
	if !rs.CanRestart(kind) {
		msg := fmt.Sprintf("%s can't restart its VPN from here.", self)
		if kind == RestartDevice {
			msg = fmt.Sprintf("%s can't be rebooted by the TapperVPN app (phones and TVs don't allow it). Restart its VPN instead, or unplug it.", self)
		}
		http.Error(w, msg, http.StatusNotImplemented)
		return
	}

	h.Logf("remotecontrol: %s restart requested by %q", kind, from)
	reply := RestartReply{OK: true, What: kind, Message: fmt.Sprintf("%s is restarting its VPN. It should be back in under a minute.", self)}
	if kind == RestartDevice {
		reply.Message = fmt.Sprintf("%s will reboot in about 30 seconds. It may take a few minutes to come back.", self)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(restartDelay)
		if err := rs.Restart(kind, from); err != nil {
			h.Logf("remotecontrol: %s restart requested by %q failed: %v", kind, from, err)
		}
	}()
}

// RestartInfo is the GET reply of the localapi restart endpoint: whether this
// device may restart others, and which devices it can ask.
type RestartInfo struct {
	Allowed bool     `json:"allowed"`
	Tag     string   `json:"tag"`
	Targets []Target `json:"targets"`
}

// serveRestart is the sending side.
//
//	GET  /localapi/v0/remotecontrol/restart                  -> RestartInfo
//	POST /localapi/v0/remotecontrol/restart?id=<stableID>&what=vpn|device -> RestartReply
func serveRestart(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	ext, ok := ipnlocal.GetExt[*Extension](h.LocalBackend())
	if !ok {
		http.Error(w, "remotecontrol extension not available", http.StatusInternalServerError)
		return
	}
	nb := ext.nodeBackend()
	switch r.Method {
	case http.MethodGet:
		if !h.PermitRead {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RestartInfo{Allowed: isRestarter(nb.Self()), Tag: RestarterTag, Targets: ext.Targets()})
		return
	case http.MethodPost:
	default:
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	if !h.PermitWrite {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	if !isRestarter(nb.Self()) {
		http.Error(w, "This device isn't allowed to restart other devices. An admin can allow it with \"Can restart others\" on the Tapper Dashboard.", http.StatusForbidden)
		return
	}
	id := r.URL.Query().Get("id")
	kind, ok := parseRestartKind(r.URL.Query().Get("what"))
	if id == "" || !ok {
		http.Error(w, "need id, and what=vpn or device", http.StatusBadRequest)
		return
	}
	peer, ok := nb.PeerByStableID(tailcfg.StableNodeID(id))
	if !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	name := peerDisplayName(peer)
	if online, known := peer.Online().GetOk(); known && !online {
		http.Error(w, fmt.Sprintf("%s is offline, so it can't be asked to restart. Restart it by hand (unplug it, or use its power button).", name), http.StatusConflict)
		return
	}
	base := nb.PeerAPIBase(peer)
	if base == "" {
		http.Error(w, fmt.Sprintf("%s can't be reached over the VPN right now.", name), http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		base+"/remotecontrol/v0/restart?what="+url.QueryEscape(string(kind)), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	client := &http.Client{Transport: h.LocalBackend().Dialer().PeerAPITransport(), Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Couldn't reach %s: %v", name, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode == http.StatusNotFound {
		// Older app without remote restart.
		http.Error(w, fmt.Sprintf("%s has an older TapperVPN that can't be restarted remotely. Update it first.", name), http.StatusNotImplemented)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, strings.TrimSpace(string(body)), resp.StatusCode)
		return
	}
	ext.logf("asked %q to restart (%s)", name, kind)
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// defaultRestarter is used by the daemon (Windows service, Linux, routers).
type defaultRestarter struct {
	b    *ipnlocal.LocalBackend
	logf logger.Logf
}

func (d *defaultRestarter) CanRestart(kind RestartKind) bool {
	switch kind {
	case RestartVPN:
		return true
	case RestartDevice:
		return canRebootMachine()
	}
	return false
}

func (d *defaultRestarter) Restart(kind RestartKind, from string) error {
	switch kind {
	case RestartDevice:
		d.logf("remotecontrol: rebooting this device, as asked by %q", from)
		return rebootMachine(from)
	case RestartVPN:
		if supervised() {
			// The service manager (Windows service parent, systemd, procd)
			// starts a fresh process, which is the most thorough restart.
			d.logf("remotecontrol: restarting the VPN service, as asked by %q", from)
			exitForRestart()
			return nil
		}
		// No supervisor to bring us back: reconnect in place instead.
		d.logf("remotecontrol: reconnecting the VPN, as asked by %q", from)
		return reconnect(d.b)
	}
	return fmt.Errorf("unknown restart kind %q", kind)
}

// reconnect stops and starts the VPN in this process, like Disconnect then
// Connect in the app.
func reconnect(b *ipnlocal.LocalBackend) error {
	if b == nil {
		return fmt.Errorf("no backend")
	}
	if !b.Prefs().WantRunning() {
		return nil // switched off on purpose; leave it off
	}
	if _, err := b.EditPrefs(&ipn.MaskedPrefs{WantRunningSet: true, Prefs: ipn.Prefs{WantRunning: false}}); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	_, err := b.EditPrefs(&ipn.MaskedPrefs{WantRunningSet: true, Prefs: ipn.Prefs{WantRunning: true}})
	return err
}
