// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"cmp"
	"slices"

	"tappervpn.com/feature"
	"tappervpn.com/ipn/ipnext"
	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/localapi"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
)

func init() {
	if !feature.Register("remotecontrol") {
		return
	}
	ipnext.RegisterExtension("remotecontrol", newExtension)
	ipnlocal.RegisterPeerAPIHandler("/remotecontrol/v0/connect", handleConnect)
	localapi.Register("remotecontrol/targets", serveTargets)
	localapi.Register("remotecontrol/connect", serveConnect)
	localapi.Register("remotecontrol/agent", serveAgent)
	localapi.Register("remotecontrol/dial", serveDial)
}

// Extension is the per-LocalBackend remote-control extension. It holds no
// session state itself (sessions and the platform agent/viewer are process-
// global machine capabilities; see hooks.go). It exists so the controller side
// can reach the node's peer list and PeerAPI dialer via ipnext.Host.
type Extension struct {
	logf logger.Logf
	host ipnext.Host
}

func newExtension(logf logger.Logf, b ipnext.SafeBackend) (ipnext.Extension, error) {
	return &Extension{logf: logger.WithPrefix(logf, "remotecontrol: ")}, nil
}

func (e *Extension) Name() string { return "remotecontrol" }

func (e *Extension) Init(h ipnext.Host) error {
	e.host = h
	return nil
}

func (e *Extension) Shutdown() error { return nil }

func (e *Extension) nodeBackend() ipnext.NodeBackend { return e.host.NodeBackend() }

// Target is a peer that can be asked to accept a control session.
type Target struct {
	StableID string `json:"stableID"`
	Name     string `json:"name"`
	OS       string `json:"os"`
}

// Targets returns the peers reachable over PeerAPI that a controller may ask to
// control. Whether a given peer will actually accept is decided by that peer's
// user at connect time (consent); this list is only who can be asked.
func (e *Extension) Targets() []Target {
	nb := e.nodeBackend()
	peers := nb.AppendMatchingPeers(nil, func(p tailcfg.NodeView) bool {
		return p.Valid() && nb.PeerHasPeerAPI(p)
	})
	ret := make([]Target, 0, len(peers))
	for _, p := range peers {
		ret = append(ret, Target{
			StableID: string(p.StableID()),
			Name:     peerDisplayName(p),
			OS:       p.Hostinfo().OS(),
		})
	}
	slices.SortFunc(ret, func(a, b Target) int { return cmp.Compare(a.Name, b.Name) })
	return ret
}

// peerDisplayName returns the friendliest available name for a peer.
func peerDisplayName(p tailcfg.NodeView) string {
	if !p.Valid() {
		return "unknown device"
	}
	if n := p.ComputedName(); n != "" {
		return n
	}
	if n := p.Name(); n != "" {
		return n
	}
	return string(p.StableID())
}
