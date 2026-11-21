// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package libtappervpn is the TapperVPN client for iOS and iPadOS. It runs
// inside the app's Network Extension (the packet tunnel), and is bound to
// Swift with gomobile:
//
//	gomobile bind -target ios,iossimulator -o Libtappervpn.xcframework ./libtappervpn
//
// Swift calls Start when the tunnel starts, Call for everything the app
// asks (status, sign in, exit node, ...; forwarded from the app with
// NETunnelProviderSession.sendProviderMessage), and Stop when it stops. The
// tunnel's network settings (addresses, routes, DNS) are applied by Swift
// when Go calls Host.ApplyNetworkSettings.
package libtappervpn

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
)

// Host is implemented in Swift by the packet tunnel provider.
type Host interface {
	// ApplyNetworkSettings applies the JSON-encoded NetworkSettings with
	// NEPacketTunnelProvider.setTunnelNetworkSettings and returns once
	// they are in place.
	ApplyNetworkSettings(settingsJSON string) error
	// DeviceName is the name this device registers with on the server.
	DeviceName() string
	// SystemVersion is UIDevice.current.systemVersion.
	SystemVersion() string
	// ModelName is the hardware model, such as "iPad13,4".
	ModelName() string
}

var (
	mu      sync.Mutex
	current *backend
)

// Start starts the VPN. dataDir is the App Group container shared with the
// app (state, logs). The tunnel's utun interface must already exist, which
// it does once Swift has applied the first network settings.
func Start(dataDir string, host Host) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in Start: %v\n%s", p, debug.Stack())
			log.Print(err)
		}
	}()
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return errors.New("already started")
	}
	setupProcess(dataDir)
	b, err := newBackend(dataDir, host)
	if err != nil {
		log.Printf("Start: %v", err)
		return err
	}
	current = b
	return nil
}

// Stop stops the VPN.
func Stop() {
	mu.Lock()
	b := current
	current = nil
	mu.Unlock()
	if b != nil {
		b.close()
	}
}

// Call runs one request from the app and returns the JSON reply. Both are
// described in api.go.
func Call(requestJSON string) string {
	var rep Reply
	func() {
		defer func() {
			if p := recover(); p != nil {
				rep = Reply{Error: fmt.Sprint("internal error: ", p)}
				log.Printf("panic in Call: %v\n%s", p, debug.Stack())
			}
		}()
		var req Request
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			rep = Reply{Error: "bad request: " + err.Error()}
			return
		}
		mu.Lock()
		b := current
		mu.Unlock()
		if b == nil {
			rep = Reply{Error: "TapperVPN isn't running"}
			return
		}
		rep = b.handle(req)
	}()
	out, _ := json.Marshal(rep)
	return string(out)
}
