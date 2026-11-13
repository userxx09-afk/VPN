// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"tappervpn.com/client/local"
)

const agentURL = "http://local-tappervpnd.sock/localapi/v0/remotecontrol/agent"

func main() {
	log.SetPrefix("tappervpn-remote: ")
	log.SetFlags(0)

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
		if err := connectAndServe(ctx, platform); err != nil && ctx.Err() == nil {
			log.Printf("link ended: %v; reconnecting", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

func connectAndServe(ctx context.Context, p Platform) error {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, pr)
	if err != nil {
		pw.Close()
		return err
	}
	req.ContentLength = -1 // streamed, full-duplex

	lc := &local.Client{}
	resp, err := lc.DoLocalRequest(req)
	if err != nil {
		pw.Close()
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		pw.Close()
		return &statusError{resp.StatusCode}
	}

	link := &readWriter{r: resp.Body, w: pw}
	defer resp.Body.Close()
	defer pw.Close()
	return runHostHelper(ctx, link, p, log.Printf)
}

type readWriter struct {
	r io.Reader
	w io.Writer
}

func (rw *readWriter) Read(p []byte) (int, error)  { return rw.r.Read(p) }
func (rw *readWriter) Write(p []byte) (int, error) { return rw.w.Write(p) }

type statusError struct{ code int }

func (e *statusError) Error() string { return "daemon returned HTTP " + itoa(e.code) }

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
