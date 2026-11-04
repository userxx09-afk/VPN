// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package identityfederation registers support for using ID tokens to
// automatically request authkeys for logging in.
package identityfederation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"tappervpn.com/feature"
	"tappervpn.com/internal/client/tappervpn"
	"tappervpn.com/ipn"
	"tappervpn.com/wif"
)

func init() {
	if !feature.Register("identityfederation") {
		return
	}
	tappervpn.HookResolveAuthKeyViaWIF.Set(resolveAuthKey)
	tappervpn.HookExchangeJWTForTokenViaWIF.Set(exchangeJWTForToken)
}

// resolveAuthKey uses OIDC identity federation to exchange the provided ID token and client ID for an authkey.
func resolveAuthKey(ctx context.Context, args tappervpn.ResolveAuthKeyWIFArgs) (string, error) {
	if args.ClientID == "" {
		return "", nil // Short-circuit, no client ID means not using identity federation
	}

	if args.IDToken == "" {
		if args.Audience == "" {
			return "", errors.New("federated identity requires either an ID token or an audience")
		}
		providerIdToken, err := wif.ObtainProviderToken(ctx, args.Audience)
		if err != nil {
			return "", errors.New("federated identity authkeys require --id-token")
		}
		args.IDToken = providerIdToken
	}
	if len(args.Tags) == 0 {
		return "", errors.New("federated identity authkeys require --advertise-tags")
	}
	if args.BaseURL == "" {
		args.BaseURL = ipn.DefaultControlURL
	}

	strippedID, ephemeral, preauth, err := parseOptionalAttributes(args.ClientID)
	if err != nil {
		return "", fmt.Errorf("failed to parse optional config attributes: %w", err)
	}

	accessToken, err := exchangeJWTForToken(ctx, tappervpn.ExchangeJWTForTokenWIFArgs{
		BaseURL:  args.BaseURL,
		ClientID: strippedID,
		IDToken:  args.IDToken,
	})
	if err != nil {
		return "", fmt.Errorf("failed to exchange JWT for access token: %w", err)
	}
	if accessToken == "" {
		return "", errors.New("received empty access token from TapperVPN")
	}

	tsClient := tappervpn.NewClient("-", tappervpn.APIKey(accessToken))
	tsClient.UserAgent = "tappervpn-cli-identity-federation"
	tsClient.BaseURL = args.BaseURL

	authkey, _, err := tsClient.CreateKey(ctx, tappervpn.KeyCapabilities{
		Devices: tappervpn.KeyDeviceCapabilities{
			Create: tappervpn.KeyDeviceCreateCapabilities{
				Reusable:      false,
				Ephemeral:     ephemeral,
				Preauthorized: preauth,
				Tags:          args.Tags,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("unexpected error while creating authkey: %w", err)
	}
	if authkey == "" {
		return "", errors.New("received empty authkey from control server")
	}

	return authkey, nil
}

func parseOptionalAttributes(clientID string) (strippedID string, ephemeral bool, preauthorized bool, err error) {
	strippedID, attrs, found := strings.Cut(clientID, "?")
	if !found {
		return clientID, true, false, nil
	}

	parsed, err := url.ParseQuery(attrs)
	if err != nil {
		return "", false, false, fmt.Errorf("failed to parse optional config attributes: %w", err)
	}

	for k := range parsed {
		switch k {
		case "ephemeral":
			ephemeral, err = strconv.ParseBool(parsed.Get(k))
		case "preauthorized":
			preauthorized, err = strconv.ParseBool(parsed.Get(k))
		default:
			return "", false, false, fmt.Errorf("unknown optional config attribute %q", k)
		}
	}
	if err != nil {
		return "", false, false, err
	}

	return strippedID, ephemeral, preauthorized, nil
}

// exchangeJWTForToken exchanges a JWT for a TapperVPN access token.
func exchangeJWTForToken(ctx context.Context, args tappervpn.ExchangeJWTForTokenWIFArgs) (string, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	token, err := (&oauth2.Config{
		Endpoint: oauth2.Endpoint{
			TokenURL: fmt.Sprintf("%s/api/v2/oauth/token-exchange", args.BaseURL),
		},
	}).Exchange(ctx, "", oauth2.SetAuthURLParam("client_id", args.ClientID), oauth2.SetAuthURLParam("jwt", args.IDToken))
	if err != nil {
		// Try to extract more detailed error message
		if retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
			return "", fmt.Errorf("token exchange failed with status %d: %s", retrieveErr.Response.StatusCode, string(retrieveErr.Body))
		}
		return "", fmt.Errorf("unexpected token exchange request error: %w", err)
	}

	return token.AccessToken, nil
}
