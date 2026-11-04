// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package peerrelay

import (
	"context"
	"fmt"
	"net/netip"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"tappervpn.com/ipn"
	tsapi "tappervpn.com/k8s-operator/apis/v1alpha1"
	"tappervpn.com/k8s-operator/reconciler"
	"tappervpn.com/k8s-operator/reconciler/tappervpnd"
	"tappervpn.com/kube/kubetypes"
)

func configSecretName(prName string, idx int32) string {
	return replicaName(prName, idx) + "-config"
}

func peerRelayHostname(pr *tsapi.PeerRelay, idx int32) string {
	prefix := string(pr.Spec.HostnamePrefix)
	if prefix == "" {
		prefix = pr.Name
	}
	return fmt.Sprintf("%s-%d", prefix, idx)
}

func peerRelayTapperVPNdConfig(pr *tsapi.PeerRelay, idx int32, endpoints []tsapi.PeerRelayEndpoint, authKey *string, loginServer string) ipn.ConfigVAlpha {
	conf := ipn.ConfigVAlpha{
		Version:         "alpha0",
		AcceptDNS:       "false",
		AcceptRoutes:    "false",
		Locked:          "false",
		Hostname:        new(peerRelayHostname(pr, idx)),
		RelayServerPort: new(peerRelayPort(pr)),
		AuthKey:         authKey,
	}

	if loginServer != "" {
		conf.ServerURL = &loginServer
	}

	for _, endpoint := range endpoints {
		addr, err := netip.ParseAddr(endpoint.Address)
		if err != nil {
			continue
		}

		conf.RelayServerStaticEndpoints = append(conf.RelayServerStaticEndpoints, netip.AddrPortFrom(addr, uint16(endpoint.Port)))
	}

	return conf
}

func (r *Reconciler) peerRelayConfigSecret(pr *tsapi.PeerRelay, idx int32, endpoints []tsapi.PeerRelayEndpoint, authKey *string, loginServer string) (*corev1.Secret, error) {
	labels := peerRelayServiceLabels(pr.Name, idx)
	return tappervpnd.NewConfigSecret(tappervpnd.ConfigSecretOptions{
		Name:      configSecretName(pr.Name, idx),
		Namespace: r.tappervpnNamespace,
		Labels:    labels,
		Config:    peerRelayTapperVPNdConfig(pr, idx, endpoints, authKey, loginServer),
	})
}

func (r *Reconciler) peerRelayStatefulSet(pr *tsapi.PeerRelay, replicas int32, pc *tsapi.ProxyClass) *appsv1.StatefulSet {
	labels := peerRelayLabels(pr.Name)
	ss := tappervpnd.NewStatefulSet(tappervpnd.StatefulSetOptions{
		Name:               resourceName(pr.Name),
		Namespace:          r.tappervpnNamespace,
		Labels:             labels,
		Image:              r.proxyImage,
		Replicas:           replicas,
		ServiceAccountName: "proxies",
		ConfigSecretNameFunc: func(idx int32) string {
			return configSecretName(pr.Name, idx)
		},
	})

	// The relay server force-sets its UDP socket buffer sizes, which requires NET_ADMIN. Without it, tappervpnd falls
	// back to the kernel defaults and relay throughput suffers. A ProxyClass security context replaces this entirely.
	ss.Spec.Template.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{"NET_ADMIN"},
		},
	}

	return tappervpnd.ApplyProxyClass(ss, pc, managedLabelKeys, nil)
}

var managedLabelKeys = []string{
	kubetypes.LabelManaged,
	reconciler.LabelParentType,
	reconciler.LabelParentName,
}

func (r *Reconciler) getProxyClass(ctx context.Context, pr *tsapi.PeerRelay) (*tsapi.ProxyClass, error) {
	if pr.Spec.ProxyClass == "" {
		return nil, nil
	}

	var pc tsapi.ProxyClass
	if err := r.Get(ctx, types.NamespacedName{Name: pr.Spec.ProxyClass}, &pc); err != nil {
		return nil, fmt.Errorf("failed to get ProxyClass %q: %w", pr.Spec.ProxyClass, err)
	}
	return &pc, nil
}
