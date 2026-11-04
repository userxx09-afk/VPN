// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"tappervpn.com/client/local"
	"tappervpn.com/ipn"
	"tappervpn.com/kube/authkey"
	"tappervpn.com/kube/egressservices"
	"tappervpn.com/kube/ingressservices"
	"tappervpn.com/kube/kubeapi"
	"tappervpn.com/kube/kubeclient"
	"tappervpn.com/kube/kubetypes"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
	"tappervpn.com/util/backoff"
)

const fieldManager = "tappervpn-container"

// kubeClient is a wrapper around TapperVPN's internal kube client that knows how to talk to the kube API server. We use
// this rather than any of the upstream Kubernetes client libaries to avoid extra imports.
type kubeClient struct {
	kubeclient.Client
	stateSecret string
	canPatch    bool // whether the client has permissions to patch Kubernetes Secrets
}

func newKubeClient(root string, stateSecret string) (*kubeClient, error) {
	if root != "/" {
		// If we are running in a test, we need to set the root path to the fake
		// service account directory.
		kubeclient.SetRootPathForTesting(root)
	}
	var err error
	kc, err := kubeclient.New("tappervpn-container")
	if err != nil {
		return nil, fmt.Errorf("error creating kube client: %w", err)
	}
	if (root != "/") || os.Getenv("TS_KUBERNETES_READ_API_SERVER_ADDRESS_FROM_ENV") == "true" {
		// Derive the API server address from the environment variables
		// Used to set http server in tests, or optionally enabled by flag
		kc.SetURL(fmt.Sprintf("https://%s:%s", os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")))
	}
	return &kubeClient{Client: kc, stateSecret: stateSecret}, nil
}

// storeDeviceID writes deviceID to 'device_id' data field of the client's state Secret.
func (kc *kubeClient) storeDeviceID(ctx context.Context, deviceID tailcfg.StableNodeID) error {
	s := &kubeapi.Secret{
		Data: map[string][]byte{
			kubetypes.KeyDeviceID: []byte(deviceID),
		},
	}
	return kc.StrategicMergePatchSecret(ctx, kc.stateSecret, s, fieldManager)
}

// storeDeviceEndpoints writes device's tailnet IPs and MagicDNS name to fields 'device_ips', 'device_fqdn' of client's
// state Secret.
func (kc *kubeClient) storeDeviceEndpoints(ctx context.Context, fqdn string, addresses []netip.Prefix) error {
	var ips []string
	for _, addr := range addresses {
		ips = append(ips, addr.Addr().String())
	}
	deviceIPs, err := json.Marshal(ips)
	if err != nil {
		return err
	}

	s := &kubeapi.Secret{
		Data: map[string][]byte{
			kubetypes.KeyDeviceFQDN: []byte(fqdn),
			kubetypes.KeyDeviceIPs:  deviceIPs,
		},
	}
	return kc.StrategicMergePatchSecret(ctx, kc.stateSecret, s, fieldManager)
}

// storeHTTPSEndpoint writes an HTTPS endpoint exposed by this device via 'tappervpn serve' to the client's state
// Secret. In practice this will be the same value that gets written to 'device_fqdn', but this should only be called
// when the serve config has been successfully set up.
func (kc *kubeClient) storeHTTPSEndpoint(ctx context.Context, ep string) error {
	s := &kubeapi.Secret{
		Data: map[string][]byte{
			kubetypes.KeyHTTPSEndpoint: []byte(ep),
		},
	}
	return kc.StrategicMergePatchSecret(ctx, kc.stateSecret, s, fieldManager)
}

// deleteAuthKey deletes the 'authkey' field of the given kube
// secret. No-op if there is no authkey in the secret.
func (kc *kubeClient) deleteAuthKey(ctx context.Context) error {
	// m is a JSON Patch data structure, see https://jsonpatch.com/ or RFC 6902.
	m := []kubeclient.JSONPatch{
		{
			Op:   "remove",
			Path: "/data/authkey",
		},
	}
	if err := kc.JSONPatchResource(ctx, kc.stateSecret, kubeclient.TypeSecrets, m); err != nil {
		if s, ok := err.(*kubeapi.Status); ok && s.Code == http.StatusUnprocessableEntity {
			// This is kubernetes-ese for "the field you asked to
			// delete already doesn't exist", aka no-op.
			return nil
		}
		return err
	}
	return nil
}

// resetContainerbootState resets state from previous runs of containerboot to
// ensure the operator doesn't use stale state when a Pod is first recreated.
//
// Device identity keys (device_id, device_fqdn, device_ips) are preserved so
// the operator can clean up the old device from the control plane.
func (kc *kubeClient) resetContainerbootState(ctx context.Context, podUID string, tappervpndConfigAuthkey string) error {
	existingSecret, err := kc.GetSecret(ctx, kc.stateSecret)
	switch {
	case kubeclient.IsNotFoundErr(err):
		// In the case that the Secret doesn't exist, we don't have any state to reset and can return early.
		return nil
	case err != nil:
		return fmt.Errorf("failed to read state Secret %q to reset state: %w", kc.stateSecret, err)
	}

	s := &kubeapi.Secret{
		Data: map[string][]byte{
			kubetypes.KeyCapVer:              fmt.Appendf(nil, "%d", tailcfg.CurrentCapabilityVersion),
			kubetypes.KeyHTTPSEndpoint:       nil,
			egressservices.KeyEgressServices: nil,
			ingressservices.IngressConfigKey: nil,
		},
	}
	if podUID != "" {
		s.Data[kubetypes.KeyPodUID] = []byte(podUID)
	}

	// Only clear reissue_authkey if the operator has actioned it.
	brokenAuthkey, ok := existingSecret.Data[kubetypes.KeyReissueAuthkey]
	if ok && tappervpndConfigAuthkey != "" && string(brokenAuthkey) != tappervpndConfigAuthkey {
		s.Data[kubetypes.KeyReissueAuthkey] = nil
	}

	return kc.StrategicMergePatchSecret(ctx, kc.stateSecret, s, fieldManager)
}

func (kc *kubeClient) setAndWaitForAuthKeyReissue(ctx context.Context, client *local.Client, cfg *settings, tappervpndConfigAuthKey string) error {
	err := client.DisconnectControl(ctx)
	if err != nil {
		return fmt.Errorf("error disconnecting from control: %w", err)
	}

	err = authkey.SetReissueAuthKey(ctx, kc.Client, kc.stateSecret, tappervpndConfigAuthKey, authkey.TapperVPNContainerFieldManager)
	if err != nil {
		return fmt.Errorf("failed to set reissue_authkey in Kubernetes Secret: %w", err)
	}

	clearFn := func(ctx context.Context) error {
		return authkey.ClearReissueAuthKey(ctx, kc.Client, kc.stateSecret, authkey.TapperVPNContainerFieldManager)
	}

	getAuthKey := func() string { return authkey.AuthKeyFromConfig(cfg.TapperVPNdConfigFilePath) }
	tappervpndCfgDir := filepath.Dir(cfg.TapperVPNdConfigFilePath)
	var notify <-chan struct{}
	if w, err := fsnotify.NewWatcher(); err != nil {
		log.Printf("auth key reissue: fsnotify unavailable, using polling: %v", err)
	} else if err := w.Add(tappervpndCfgDir); err != nil {
		w.Close()
		log.Printf("auth key reissue: fsnotify watch failed, using polling: %v", err)
	} else {
		defer w.Close()
		ch := make(chan struct{}, 1)
		toWatch := filepath.Join(tappervpndCfgDir, "..data")
		go func() {
			for ev := range w.Events {
				if ev.Name == toWatch {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}()
		notify = ch
		log.Printf("auth key reissue: watching for config changes via fsnotify")
	}

	err = authkey.WaitForAuthKeyReissue(ctx, tappervpndConfigAuthKey, 10*time.Minute, getAuthKey, clearFn, notify)
	if err != nil {
		return fmt.Errorf("failed to receive new auth key: %w", err)
	}

	return nil
}

// waitForConsistentState waits for tappervpnd to finish writing state if it
// looks like it's started. It is designed to reduce the likelihood that
// tappervpnd gets shut down in the window between authenticating to control
// and finishing writing state. However, it's not bullet proof because we can't
// atomically authenticate and write state.
func (kc *kubeClient) waitForConsistentState(ctx context.Context) error {
	var logged bool

	bo := backoff.NewBackoff("", logger.Discard, 2*time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		secret, err := kc.GetSecret(ctx, kc.stateSecret)
		if ctx.Err() != nil || kubeclient.IsNotFoundErr(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("getting Secret %q: %v", kc.stateSecret, err)
		}

		if hasConsistentState(secret.Data) {
			return nil
		}

		if !logged {
			log.Printf("Waiting for tappervpnd to finish writing state to Secret %q", kc.stateSecret)
			logged = true
		}
		bo.BackOff(ctx, errors.New("")) // Fake error to trigger actual sleep.
	}
}

// hasConsistentState returns true is there is either no state or the full set
// of expected keys are present.
func hasConsistentState(d map[string][]byte) bool {
	var (
		_, hasCurrent = d[string(ipn.CurrentProfileStateKey)]
		_, hasKnown   = d[string(ipn.KnownProfilesStateKey)]
		_, hasMachine = d[string(ipn.MachineKeyStateKey)]
		hasProfile    bool
	)

	for k := range d {
		if strings.HasPrefix(k, "profile-") {
			if hasProfile {
				return false // We only expect one profile.
			}
			hasProfile = true
		}
	}

	// Approximate check, we don't want to reimplement all of profileManager.
	return (hasCurrent && hasKnown && hasMachine && hasProfile) ||
		(!hasCurrent && !hasKnown && !hasMachine && !hasProfile)
}
