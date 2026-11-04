// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js && !ts_omit_acme

package tappervpn

import (
	"context"
	"crypto/tls"

	"tappervpn.com/client/local"
)

// GetCertificate is an alias for [tappervpn.com/client/local.GetCertificate].
//
// Deprecated: import [tappervpn.com/client/local] instead and use [local.Client.GetCertificate].
func GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return local.GetCertificate(hi)
}

// CertPair is an alias for [tappervpn.com/client/local.CertPair].
//
// Deprecated: import [tappervpn.com/client/local] instead and use [local.Client.CertPair].
func CertPair(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	return local.CertPair(ctx, domain)
}

// ExpandSNIName is an alias for [tappervpn.com/client/local.ExpandSNIName].
//
// Deprecated: import [tappervpn.com/client/local] instead and use [local.Client.ExpandSNIName].
func ExpandSNIName(ctx context.Context, name string) (fqdn string, ok bool) {
	return local.ExpandSNIName(ctx, name)
}
