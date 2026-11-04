// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package local

import "context"

// DebugRejects returns the raw JSON body served by the debug-rejects
// LocalAPI endpoint. Callers that want a typed response can decode the
// returned bytes into [tappervpn.com/net/connreject.DebugRejectsResponse].
//
// The response body is returned as opaque bytes so this method does
// not import tappervpn.com/net/connreject; that lets builds which
// don't need the typed struct (such as the tappervpn CLI) keep
// connreject out of their dependency graph.
func (lc *Client) DebugRejects(ctx context.Context) ([]byte, error) {
	return lc.get200(ctx, "/localapi/v0/debug-rejects")
}
