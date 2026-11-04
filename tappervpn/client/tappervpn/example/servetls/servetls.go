// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// The servetls program shows how to run an HTTPS server
// using a TapperVPN cert via LetsEncrypt.
package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"

	"tappervpn.com/client/local"
)

func main() {
	var lc local.Client
	s := &http.Server{
		TLSConfig: &tls.Config{
			GetCertificate: lc.GetCertificate,
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<h1>Hello from TapperVPN!</h1> It works.")
		}),
	}
	log.Printf("Running TLS server on :443 ...")
	log.Fatal(s.ListenAndServeTLS("", ""))
}
