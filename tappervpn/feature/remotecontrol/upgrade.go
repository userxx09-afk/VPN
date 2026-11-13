// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"tappervpn.com/net/netutil"
)

// upgradeProto is the HTTP Upgrade token used by every bidirectional
// remote-control link (helper<->daemon and controller<->host). The links are
// full-duplex, and Go's HTTP/1 client cannot stream a request body while
// reading the response (it writes the whole request first). So instead of a
// streaming request body, each link makes a bodiless request that the server
// answers with 101 Switching Protocols and hijacks into a raw bidirectional
// connection.
const upgradeProto = "ts-remotecontrol"

// hijackUpgrade is the server side: it answers an Upgrade request with 101 and
// hijacks the connection, returning a raw net.Conn for the bidirectional frame
// protocol. Any bytes the client already sent after the request are preserved.
func hijackUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("connection does not support hijacking (need HTTP/1)")
	}
	w.Header().Set("Upgrade", upgradeProto)
	w.Header().Set("Connection", "upgrade")
	w.WriteHeader(http.StatusSwitchingProtocols)
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return netutil.NewDrainBufConn(conn, brw.Reader), nil
}

// httpStatusError reports a non-101 response from an upgrade dial, carrying the
// status so callers can distinguish a decline (403) from other failures.
type httpStatusError struct {
	code int
	msg  string
}

func (e *httpStatusError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("HTTP %d: %s", e.code, e.msg)
	}
	return fmt.Sprintf("HTTP %d", e.code)
}

// dialUpgradeHTTP is the client side of hijackUpgrade over an arbitrary
// http.Client (used for peer dials over the PeerAPI transport). It sends a
// bodiless Upgrade request and, on 101, returns the raw bidirectional stream.
// On any other status it returns an *httpStatusError.
func dialUpgradeHTTP(ctx context.Context, client *http.Client, url string) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Upgrade", upgradeProto)
	req.Header.Set("Connection", "upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &httpStatusError{code: resp.StatusCode, msg: strings.TrimSpace(string(msg))}
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("server did not upgrade to a writable stream")
	}
	return rwc, nil
}
