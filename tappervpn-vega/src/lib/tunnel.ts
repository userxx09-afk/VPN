// The WireGuard tunnel this TV asks the system for, from the server's
// settings and the TV's own private key.

import type {BridgeConfig} from './pairing';

/**
 * all:  every app's traffic goes through TapperVPN (out through the server
 *       or its exit node).
 * mesh: only traffic to your TapperVPN devices; the rest goes out directly.
 */
export type RouteMode = 'all' | 'mesh';

export interface TunnelSettings {
  name: string;
  privateKey: string;
  addresses: string[];
  dns: string[];
  mtu: number;
  routes: string[];
  peer: {
    publicKey: string;
    endpointHost: string;
    endpointPort: number;
    allowedIPs: string[];
    persistentKeepalive: number;
  };
}

export function tunnelSettings(
  cfg: BridgeConfig,
  privateKey: string,
  mode: RouteMode,
): TunnelSettings {
  const i = cfg.peer.endpoint.lastIndexOf(':');
  const routes =
    mode === 'mesh' && cfg.peer.meshIPs.length > 0 ? cfg.peer.meshIPs : cfg.peer.allowedIPs;
  return {
    name: 'TapperVPN',
    privateKey,
    addresses: [cfg.address],
    // In mesh mode the TV keeps using its own DNS: names on the internet
    // shouldn't depend on the tunnel being up.
    dns: mode === 'all' ? cfg.dns : [],
    mtu: cfg.mtu,
    routes,
    peer: {
      publicKey: cfg.peer.publicKey,
      endpointHost: cfg.peer.endpoint.slice(0, i),
      endpointPort: Number(cfg.peer.endpoint.slice(i + 1)),
      allowedIPs: routes,
      persistentKeepalive: cfg.peer.persistentKeepalive,
    },
  };
}

/** The same tunnel as a wg-quick file, e.g. for another WireGuard app. */
export function wgQuickConfig(t: TunnelSettings): string {
  const lines = [
    '[Interface]',
    `PrivateKey = ${t.privateKey}`,
    `Address = ${t.addresses.join(', ')}`,
  ];
  if (t.dns.length) {
    lines.push(`DNS = ${t.dns.join(', ')}`);
  }
  lines.push(
    `MTU = ${t.mtu}`,
    '',
    '[Peer]',
    `PublicKey = ${t.peer.publicKey}`,
    `Endpoint = ${t.peer.endpointHost}:${t.peer.endpointPort}`,
    `AllowedIPs = ${t.peer.allowedIPs.join(', ')}`,
    `PersistentKeepalive = ${t.peer.persistentKeepalive}`,
    '',
  );
  return lines.join('\n');
}
