// Talks to the control server's WireGuard bridge endpoints
// (headscale/hscontrol/wgbridge.go):
//
//   POST /tappervpn/wg/pair   {publicKey, name} -> {status: "pending", code, expiresIn}
//                                                | {status: "approved"}
//   GET  /tappervpn/wg/config?key=<publicKey>   -> 200 {status: "approved", config}
//                                                | 202 {status: "pending"}
//                                                | 404 {status: "unknown"}

import {isWireGuardKey} from './keys';

export const DEFAULT_SERVER = 'https://vp.tapper.co';

/** What wg-bridge.sh writes for a paired TV. */
export interface BridgeConfig {
  name: string;
  address: string; // "10.77.0.2/32"
  dns: string[];
  mtu: number;
  peer: {
    publicKey: string;
    endpoint: string; // "vp.tapper.co:51821"
    allowedIPs: string[]; // everything: ["0.0.0.0/0"]
    meshIPs: string[]; // only your devices: ["100.64.0.0/10", "10.77.0.0/24"]
    persistentKeepalive: number;
  };
}

export type PairStart =
  | {status: 'pending'; code: string; expiresIn: number}
  | {status: 'approved'};

export type PairState =
  | {status: 'approved'; config: BridgeConfig}
  | {status: 'pending'}
  | {status: 'unknown'};

export type Fetch = (url: string, init?: {method?: string; headers?: Record<string, string>; body?: string}) => Promise<{
  status: number;
  json(): Promise<unknown>;
}>;

export class PairingError extends Error {}

/** "vp.tapper.co" or "https://vp.tapper.co/" -> "https://vp.tapper.co". */
export function normalizeServer(s: string): string {
  let v = s.trim();
  if (!v) {
    return DEFAULT_SERVER;
  }
  if (!/^https?:\/\//i.test(v)) {
    v = 'https://' + v;
  }
  return v.replace(/\/+$/, '');
}

function asObject(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : {};
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
}

const CIDR4 = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;

/** Checks the server's answer, so a bad file can't produce a broken tunnel. */
export function parseConfig(v: unknown): BridgeConfig {
  const c = asObject(v);
  const p = asObject(c.peer);
  const cfg: BridgeConfig = {
    name: typeof c.name === 'string' ? c.name : 'Fire TV',
    address: typeof c.address === 'string' ? c.address : '',
    dns: strings(c.dns),
    mtu: typeof c.mtu === 'number' && c.mtu >= 1280 && c.mtu <= 1500 ? c.mtu : 1280,
    peer: {
      publicKey: typeof p.publicKey === 'string' ? p.publicKey : '',
      endpoint: typeof p.endpoint === 'string' ? p.endpoint : '',
      allowedIPs: strings(p.allowedIPs).filter(x => CIDR4.test(x)),
      meshIPs: strings(p.meshIPs).filter(x => CIDR4.test(x)),
      persistentKeepalive:
        typeof p.persistentKeepalive === 'number' ? p.persistentKeepalive : 25,
    },
  };
  if (!CIDR4.test(cfg.address)) {
    throw new PairingError('The server sent no address for this TV.');
  }
  if (!isWireGuardKey(cfg.peer.publicKey)) {
    throw new PairingError("The server's WireGuard key is missing.");
  }
  if (!/^[^\s:]+:\d+$/.test(cfg.peer.endpoint)) {
    throw new PairingError("The server's WireGuard address is missing.");
  }
  if (cfg.peer.allowedIPs.length === 0) {
    cfg.peer.allowedIPs = ['0.0.0.0/0'];
  }
  return cfg;
}

export class PairingClient {
  constructor(
    private server: string,
    private fetchFn: Fetch,
  ) {
    this.server = normalizeServer(server);
  }

  private async call(path: string, init?: Parameters<Fetch>[1]) {
    let res;
    try {
      res = await this.fetchFn(this.server + path, init);
    } catch {
      throw new PairingError(`Can't reach ${this.server}. Check the TV's internet connection.`);
    }
    let body: unknown = {};
    try {
      body = await res.json();
    } catch {
      // Not JSON: an old server, or something else at that address.
    }
    const obj = asObject(body);
    // A plain 404 (not our {"status":"unknown"}): the bridge is off.
    if (res.status === 404 && obj.status !== 'unknown') {
      throw new PairingError(
        'The server has no WireGuard bridge yet. On the server run: sudo wg-bridge install',
      );
    }
    return {status: res.status, body: obj};
  }

  async start(publicKey: string, name: string): Promise<PairStart> {
    const {status, body} = await this.call('/tappervpn/wg/pair', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({publicKey, name}),
    });
    if (status === 200 && body.status === 'approved') {
      return {status: 'approved'};
    }
    if (status === 200 && body.status === 'pending' && typeof body.code === 'string') {
      return {
        status: 'pending',
        code: body.code,
        expiresIn: typeof body.expiresIn === 'number' ? body.expiresIn : 900,
      };
    }
    if (status === 429) {
      throw new PairingError('Too many devices are waiting to pair. Try again in 15 minutes.');
    }
    const err = typeof body.error === 'string' ? body.error : `error ${status}`;
    throw new PairingError(`The server refused to pair: ${err}`);
  }

  async state(publicKey: string): Promise<PairState> {
    const {status, body} = await this.call(
      '/tappervpn/wg/config?key=' + encodeURIComponent(publicKey),
    );
    if (status === 200 && body.status === 'approved') {
      return {status: 'approved', config: parseConfig(body.config)};
    }
    if (status === 202) {
      return {status: 'pending'};
    }
    if (status === 404) {
      return {status: 'unknown'};
    }
    throw new PairingError(`The server answered with error ${status}.`);
  }
}
