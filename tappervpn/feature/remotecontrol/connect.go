// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

	// Stream the request body via a pipe so we can write frames to the host
	// while concurrently reading its response.
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/remotecontrol/v0/connect", pr)
	if err != nil {
		pw.Close()
		viewer.Close()
		return err
	}
	req.ContentLength = -1 // chunked: unbounded duplex stream

	client := &http.Client{Transport: b.Dialer().PeerAPITransport()}
	resp, err := client.Do(req)
	if err != nil {
		pw.Close()
		viewer.Close()
		return fmt.Errorf("dialing %q: %w", peerDisplayName(peer), err)
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		pw.Close()
		viewer.Close()
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%s declined the connection", peerDisplayName(peer))
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	conn := rwPair{
		Reader: resp.Body,
		Writer: pw,
		closer: func() error { pw.Close(); return resp.Body.Close() },
	}
	defer conn.Close()
	// RunControllerSession closes the viewer on return.
	return RunControllerSession(ctx, conn, viewer, logf)
}
