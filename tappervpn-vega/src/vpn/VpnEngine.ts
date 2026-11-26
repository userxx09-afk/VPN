// The one place that turns the tunnel on and off.
//
// Vega OS has a system VPN ("VPN Manager API", WireGuard only, IPv4 only,
// one tunnel per device), but Amazon hasn't published it for every app: it
// isn't in the public Vega API reference or on npm. Until this app gets
// access, the system engine reports itself unavailable and the app says so
// instead of pretending to connect. When Amazon provides the package, only
// system.ts needs to change.

import type {TunnelSettings} from '../lib/tunnel';

export type VpnStatus = 'disconnected' | 'connecting' | 'connected' | 'disconnecting' | 'error';

export interface VpnEngine {
  /** Why it can't be used, or null when it can. */
  unavailableReason(): Promise<string | null>;
  connect(t: TunnelSettings): Promise<void>;
  disconnect(): Promise<void>;
  status(): Promise<VpnStatus>;
  /** Calls back on every change; returns the function that stops it. */
  subscribe(cb: (s: VpnStatus, detail?: string) => void): () => void;
}

export const NOT_AVAILABLE =
  "This Fire TV doesn't let TapperVPN start a VPN yet. Amazon only gives its VPN " +
  'API to approved apps; until TapperVPN is approved, the TV stays paired and ready.';

/** Used when the system has no VPN API for this app. */
export class UnavailableEngine implements VpnEngine {
  constructor(private reason = NOT_AVAILABLE) {}
  async unavailableReason() {
    return this.reason;
  }
  async connect(): Promise<void> {
    throw new Error(this.reason);
  }
  async disconnect() {}
  async status(): Promise<VpnStatus> {
    return 'disconnected';
  }
  subscribe() {
    return () => {};
  }
}
