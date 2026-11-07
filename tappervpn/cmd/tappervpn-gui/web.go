// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"bytes"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"strings"

	"github.com/toqueteos/webbrowser"
	"tappervpn.com/tailcfg"
)

//go:embed ui/index.html
var indexHTML []byte

// webServer serves the TapperVPN window on 127.0.0.1. Every request must
// carry the random token from the URL the app opened (path for the page,
// header or query for the API), and the Host header must be the loopback
// address, so neither web pages nor other Windows users can drive it.
type webServer struct {
	hub   *hub
	token string
	addr  string // 127.0.0.1:port
}

func (s *webServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.addr {
		http.Error(w, "bad host", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")

	prefix := "/t/" + s.token + "/"
	switch {
	case r.URL.Path == prefix:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
		w.Write(indexHTML)
		return
	case r.URL.Path == prefix+"icon.png":
		var buf bytes.Buffer
		png.Encode(&buf, drawLogo(64, strokeColor[stateOn]))
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
		return
	case strings.HasPrefix(r.URL.Path, "/api/"):
	default:
		http.NotFound(w, r)
		return
	}

	tok := r.Header.Get("X-TapperVPN-Token")
	if r.URL.Path == "/api/events" {
		tok = r.URL.Query().Get("token") // EventSource can't set headers
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if r.URL.Path == "/api/events" {
		s.events(w, r)
		return
	}
	if r.URL.Path == "/api/state" {
		writeJSON(w, s.hub.Model())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		On     bool   `json:"on"`
		ID     string `json:"id"`
		Server string `json:"server"`
		Key    string `json:"key"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req)

	h := s.hub
	var err error
	switch r.URL.Path {
	case "/api/running":
		err = h.SetRunning(req.On)
	case "/api/exit-node":
		err = h.SetExitNode(tailcfg.StableNodeID(req.ID))
	case "/api/allow-lan":
		err = h.SetAllowLAN(req.On)
	case "/api/sign-in":
		err = h.SignIn(req.Server, req.Key)
	case "/api/sign-out":
		err = h.SignOut()
	case "/api/update/check":
		go h.checkUpdate()
	case "/api/update/install":
		err = h.InstallUpdate()
	case "/api/open":
		// Links open in the normal browser, not in the app window.
		switch req.ID {
		case "admin":
			err = webbrowser.Open(h.Model().AdminURL)
		case "auth":
			if u := h.Model().AuthURL; u != "" {
				err = webbrowser.Open(u)
			}
		case "releases":
			err = webbrowser.Open("https://github.com/userxx09-afk/VPN/releases")
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *webServer) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch, done := s.hub.Subscribe()
	defer done()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			b, _ := json.Marshal(m)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
