// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"tappervpn.com/client/local"
	"tappervpn.com/paths"
	"tappervpn.com/updater"
)

// dialUpgrade opens a raw bidirectional connection to a local daemon endpoint by
// HTTP Upgrade. The remote-control links are full-duplex and Go's HTTP/1 client
// can't stream a request body while reading the response, so the daemon answers
// with 101 Switching Protocols and we use the hijacked stream directly.
func dialUpgrade(ctx context.Context, url string) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Upgrade", "ts-remotecontrol")
	req.Header.Set("Connection", "upgrade")
	lc := &local.Client{Socket: paths.DefaultTapperVPNdSocket()}
	resp, err := lc.DoLocalRequest(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &statusError{code: resp.StatusCode, msg: strings.TrimSpace(string(b))}
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("daemon did not upgrade to a writable stream")
	}
	return rwc, nil
}

const agentURL = "http://local-tappervpnd.sock/localapi/v0/remotecontrol/agent"

func main() {
	log.SetPrefix("tappervpn-remote: ")
	log.SetFlags(0)
	setupLog()
	if !singleInstance() {
		log.Printf("another helper is already running in this session; exiting")
		return
	}

	log.Printf("starting %s; daemon socket=%s", updater.Version, paths.DefaultTapperVPNdSocket())

	platform, err := newHostPlatform()
	if err != nil {
		log.Fatalf("%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Stay attached to the daemon, reconnecting if it restarts or the link
	// drops. The daemon only advertises this node as controllable while a
	// helper is attached.
	for ctx.Err() == nil {
		if err := connectAndServe(ctx, platform, log.Printf); err != nil && ctx.Err() == nil {
			log.Printf("link ended: %v; reconnecting", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

func connectAndServe(ctx context.Context, p Platform, logf func(string, ...any)) error {
	// Upgrade the agent link to a raw bidirectional connection. A streaming
	// request body can't carry a duplex link over Go's HTTP/1 client, so the
	// daemon answers with 101 and we use the raw stream (see dialUpgrade).
	conn, err := dialUpgrade(ctx, agentURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	logf("attached to daemon; serving link")

	lw := &linkWriter{w: conn}
	if err := sendInfo(lw, p); err != nil {
		return err
	}
	return runHostHelper(ctx, conn, lw, p, logf)
}

// statusError is a non-101 answer from the daemon, with its message.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string {
	if e.msg != "" {
		return "daemon returned HTTP " + itoa(e.code) + ": " + e.msg
	}
	return "daemon returned HTTP " + itoa(e.code)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
