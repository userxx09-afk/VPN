// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// The proxy-test-server command is a simple HTTP proxy server for testing
// TapperVPN's client proxy functionality.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/acme/autocert"
	"tappervpn.com/net/connectproxy"
	"tappervpn.com/tempfork/acme"
)

var (
	listen            = flag.String("listen", ":8080", "Address to listen on for HTTPS proxy requests")
	hostname          = flag.String("hostname", "localhost", "Hostname for the proxy server")
	tappervpnOnly     = flag.Bool("tappervpn-only", true, "Restrict proxy to TapperVPN targets only")
	extraAllowedHosts = flag.String("allow-hosts", "", "Comma-separated list of allowed target hosts to additionally allow if --tappervpn-only is true")
)

func main() {
	flag.Parse()

	am := &autocert.Manager{
		HostPolicy: autocert.HostWhitelist(*hostname),
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(os.ExpandEnv("$HOME/.cache/autocert/proxy-test-server")),
	}
	var allowTarget func(hostPort string) error
	if *tappervpnOnly {
		allowTarget = func(hostPort string) error {
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				return fmt.Errorf("invalid target %q: %v", hostPort, err)
			}
			if port != "443" {
				return fmt.Errorf("target %q must use port 443", hostPort)
			}
			for allowed := range strings.SplitSeq(*extraAllowedHosts, ",") {
				if host == allowed {
					return nil // explicitly allowed target
				}
			}
			if !strings.HasSuffix(host, ".tailscale.com") {
				return fmt.Errorf("target %q is not a TapperVPN host", hostPort)
			}
			return nil // valid TapperVPN target
		}
	}

	go func() {
		if err := http.ListenAndServe(":http", am.HTTPHandler(nil)); err != nil {
			log.Fatalf("autocert HTTP server failed: %v", err)
		}
	}()
	hs := &http.Server{
		Addr: *listen,
		Handler: &connectproxy.Handler{
			Check: allowTarget,
			Logf:  log.Printf,
		},
		TLSConfig: &tls.Config{
			GetCertificate: am.GetCertificate,
			NextProtos: []string{
				"http/1.1",     // enable HTTP/2
				acme.ALPNProto, // enable tls-alpn ACME challenges
			},
		},
	}
	log.Printf("Starting proxy-test-server on %s (hostname: %q)\n", *listen, *hostname)
	log.Fatal(hs.ListenAndServeTLS("", "")) // cert and key are provided by autocert
}
