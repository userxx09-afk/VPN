// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"io"
	"net/http"
)

// PeerAPI gives us a single HTTP request to work with, not a raw socket, so a
// session runs full-duplex over it: the controller streams frames in the
// request body while the host streams frames back in a flushed response body.
// This matches how other PeerAPI streaming (e.g. SSH-over-PeerAPI) works and
// avoids depending on http.Hijacker, which the PeerAPI server does not offer.

// flushWriter writes to an http.ResponseWriter and flushes after every write so
// host→controller frames are delivered immediately rather than buffered.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil && n > 0 {
		fw.f.Flush()
	}
	return n, err
}

// rwPair adapts a separate reader and writer into one io.ReadWriteCloser, with
// an optional close function that tears both ends down.
type rwPair struct {
	io.Reader
	io.Writer
	closer func() error
}

func (p rwPair) Close() error {
	if p.closer != nil {
		return p.closer()
	}
	return nil
}
