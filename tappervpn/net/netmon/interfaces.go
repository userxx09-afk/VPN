// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package netmon

import (
	"errors"
	"net"

	"tappervpn.com/syncs"
)

type ifProps struct {
	mu    syncs.Mutex
	name  string // interface name, if known/set
	index int    // interface index, if known/set
}

// tsIfProps tracks the properties (name and index) of the tappervpn interface.
// There is only one tappervpn interface per tappervpnd instance.
var tsIfProps ifProps

func (p *ifProps) tsIfName() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

func (p *ifProps) tsIfIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index
}

func (p *ifProps) set(ifName string, ifIndex int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name = ifName
	p.index = ifIndex
}

// TODO (barnstar): This doesn't need the Monitor receiver anymore but we're
// keeping it for API compatibility to avoid a breaking change.  This can be
// removed when the various clients have switched to SetTapperVPNInterfaceProps
func (m *Monitor) SetTapperVPNInterfaceName(ifName string) {
	SetTapperVPNInterfaceProps(ifName, 0)
}

// SetTapperVPNInterfaceProps sets the name of the TapperVPN interface and
// its index for use by various listeners/dialers.  If the index is zero,
// an attempt will be made to look it up by name.  This makes no attempt
// to validate that the interface exists at the time of calling.
//
// If this method is called, it is the responsibility of the caller to
// update the interface name and index if they change.
//
// This should be called as early as possible during tappervpnd startup.
func SetTapperVPNInterfaceProps(ifName string, ifIndex int) {
	if ifIndex != 0 {
		tsIfProps.set(ifName, ifIndex)
		return
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, iface := range ifaces {
		if iface.Name == ifName {
			ifIndex = iface.Index
			break
		}
	}

	tsIfProps.set(ifName, ifIndex)
}

// TapperVPNInterfaceName returns the name of the TapperVPN interface.
// For example, "tappervpn0", "tun0", "utun3", etc or an error if unset.
//
// Callers must handle errors, as the TapperVPN interface
// name may not be set in some environments.
func TapperVPNInterfaceName() (string, error) {
	name := tsIfProps.tsIfName()
	if name == "" {
		return "", errors.New("TapperVPN interface name not set")
	}
	return name, nil
}

// TapperVPNInterfaceIndex returns the index of the TapperVPN interface or
// an error if unset.
//
// Callers must handle errors, as the TapperVPN interface
// index may not be set in some environments.
func TapperVPNInterfaceIndex() (int, error) {
	index := tsIfProps.tsIfIndex()
	if index == 0 {
		return 0, errors.New("TapperVPN interface index not set")
	}
	return index, nil
}
