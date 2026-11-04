// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package main

import (
	"fmt"
	"net/url"
	"os"

	"go.uber.org/zap"
	tappervpn "tailscale.com/client/tailscale/v2"

	"tappervpn.com/ipn"
)

const (
	oidcJWTPath = "/var/run/secrets/tappervpn/serviceaccount/token"
)

func newTSClient(logger *zap.SugaredLogger, clientID, clientIDPath, clientSecretPath, loginServer string) (*tappervpn.Client, error) {
	baseURL := ipn.DefaultControlURL
	if loginServer != "" {
		baseURL = loginServer
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	client := &tappervpn.Client{
		UserAgent: "tappervpn-k8s-operator",
		BaseURL:   base,
	}

	if clientID == "" {
		// Use static client credentials mounted to disk.
		clientIDBytes, err := os.ReadFile(clientIDPath)
		if err != nil {
			return nil, fmt.Errorf("error reading client ID %q: %w", clientIDPath, err)
		}
		clientSecretBytes, err := os.ReadFile(clientSecretPath)
		if err != nil {
			return nil, fmt.Errorf("reading client secret %q: %w", clientSecretPath, err)
		}

		client.Auth = &tappervpn.OAuth{
			ClientID:     string(clientIDBytes),
			ClientSecret: string(clientSecretBytes),
		}
	} else {
		// Use workload identity federation.
		client.Auth = &tappervpn.IdentityFederation{
			ClientID: clientID,
			IDTokenFunc: func() (string, error) {
				token, err := os.ReadFile(oidcJWTPath)
				if err != nil {
					return "", err
				}

				return string(token), nil
			},
		}
	}

	return client, nil
}
