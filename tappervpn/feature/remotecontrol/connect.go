// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
)

// Connect is the controller side: it dials target peer's PeerAPI, and on
// acceptance runs an in-process controller session driving the registered
// viewer. This in-process path serves platforms whose viewer lives in the same
// process as the Go core (e.g. Android, via a JNI-backed viewer). On Windows,
// where the viewer lives in a separate user-session helper, the daemon instead
// brokers the stream to the helper over the agent channel (see localapi.go);
// that path does not call Connect.
func Connect(ctx context.Context, b *ipnlocal.LocalBackend, stableID string, logf logger.Logf) error {
	maker := getViewerMaker()
	if maker == nil {
		return errors.New("this device cannot act as a controller (no viewer registered)")
	}
	viewer, err := maker()
	if err != nil {
		return fmt.Errorf("starting viewer: %w", err)
	}
	return ConnectWithViewer(ctx, b, stableID, viewer, logf)
}

// ConnectWithViewer is like Connect but drives the caller-supplied viewer
// rather than one from the registered maker. It is used by platform bridges
// (e.g. Android) that construct a viewer per connection. The viewer is closed
// when the session ends.
func ConnectWithViewer(ctx context.Context, b *ipnlocal.LocalBackend, stableID string, viewer Viewer, logf logger.Logf) error {
	ext, ok := ipnlocal.GetExt[*Extension](b)
	if !ok {
		viewer.Close()
		return errors.New("remotecontrol extension not available")
	}

	nb := ext.nodeBackend()
	peer, ok := nb.PeerByStableID(tailcfg.StableNodeID(stableID))
	if !ok {
		viewer.Close()
		return fmt.Errorf("peer %q not found", stableID)
	}
	base := nb.PeerAPIBase(peer)
	if base == "" {
		viewer.Close()
		return fmt.Errorf("peer %q is not reachable over PeerAPI", stableID)
	}

	// Upgrade to a raw bidirectional connection. The host prompts for consent
	// before switching protocols, so a decline arrives as a 403 (not a 101).
	client := &http.Client{Transport: b.Dialer().PeerAPITransport()}
	conn, err := dialUpgradeHTTP(ctx, client, base+"/remotecontrol/v0/connect")
	if err != nil {
		viewer.Close()
		var se *httpStatusError
		if errors.As(err, &se) && se.code == http.StatusForbidden {
			return fmt.Errorf("%s declined the connection", peerDisplayName(peer))
		}
		return fmt.Errorf("dialing %q: %w", peerDisplayName(peer), err)
	}
	defer conn.Close()
	// RunControllerSession closes the viewer on return.
	return RunControllerSession(ctx, conn, viewer, logf)
}
