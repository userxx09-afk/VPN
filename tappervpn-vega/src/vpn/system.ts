// Vega OS system VPN, through the native module Amazon ships to approved
// VPN apps.
//
// What is known (Vega 2.0 release notes, Nov 2026): the "VPN Manager API"
// supports WireGuard only, doesn't route IPv6, and has an addRoute(cidr)
// call. Its package name and exact calls aren't public, so this adapter
// looks the module up at run time and maps our TunnelSettings onto the calls
// below. When Amazon's package arrives, adjust MODULE_NAMES and the
// SystemVpnModule shape to match it; nothing else in the app changes.

import {NativeEventEmitter, NativeModules, TurboModuleRegistry} from 'react-native';

import type {TunnelSettings} from '../lib/tunnel';
import {NOT_AVAILABLE, UnavailableEngine, type VpnEngine, type VpnStatus} from './VpnEngine';

const MODULE_NAMES = ['VpnManager', 'KeplerVpnManager', 'VegaVpnManager'];

/** The calls we expect; to be checked against Amazon's package. */
interface SystemVpnModule {
  /** Shows the one-time system "allow VPN" prompt if needed. */
  requestPermission?(): Promise<boolean>;
  configureWireGuard(cfg: {
    sessionName: string;
    privateKey: string;
    addresses: string[];
    dnsServers: string[];
    mtu: number;
    peers: {
      publicKey: string;
      endpoint: string;
      allowedIPs: string[];
      persistentKeepalive: number;
    }[];
  }): Promise<void>;
  addRoute(cidr: string): Promise<void>;
  start(): Promise<void>;
  stop(): Promise<void>;
  getStatus(): Promise<string>;
}

function findModule(): SystemVpnModule | null {
  for (const name of MODULE_NAMES) {
    let m: unknown = null;
    try {
      m = TurboModuleRegistry.get(name);
    } catch {
      m = null;
    }
    m = m ?? (NativeModules as Record<string, unknown>)[name];
    const mod = m as Partial<SystemVpnModule> | null;
    if (mod && typeof mod.configureWireGuard === 'function' && typeof mod.start === 'function') {
      return mod as SystemVpnModule;
    }
  }
  return null;
}

function toStatus(s: string): VpnStatus {
  const v = s.toLowerCase();
  if (v.includes('disconnecting') || v.includes('stopping')) {
    return 'disconnecting';
  }
  if (v.includes('disconnect') || v.includes('stop') || v.includes('idle')) {
    return 'disconnected';
  }
  if (v.includes('connecting') || v.includes('starting')) {
    return 'connecting';
  }
  if (v.includes('connect') || v.includes('up') || v.includes('running')) {
    return 'connected';
  }
  return 'error';
}

class SystemEngine implements VpnEngine {
  private listeners = new Set<(s: VpnStatus, detail?: string) => void>();

  constructor(private mod: SystemVpnModule) {
    try {
      const emitter = new NativeEventEmitter(mod as never);
      emitter.addListener('VpnStatusChanged', (e: {status?: string; error?: string}) =>
        this.emit(toStatus(String(e?.status ?? '')), e?.error),
      );
    } catch {
      // No events: the app polls status() instead.
    }
  }

  private emit(s: VpnStatus, detail?: string) {
    this.listeners.forEach(cb => cb(s, detail));
  }

  async unavailableReason() {
    return null;
  }

  async connect(t: TunnelSettings) {
    this.emit('connecting');
    try {
      if (this.mod.requestPermission && !(await this.mod.requestPermission())) {
        throw new Error('The VPN permission was declined. Choose Connect again to allow it.');
      }
      await this.mod.configureWireGuard({
        sessionName: t.name,
        privateKey: t.privateKey,
        addresses: t.addresses,
        dnsServers: t.dns,
        mtu: t.mtu,
        peers: [
          {
            publicKey: t.peer.publicKey,
            endpoint: `${t.peer.endpointHost}:${t.peer.endpointPort}`,
            allowedIPs: t.peer.allowedIPs,
            persistentKeepalive: t.peer.persistentKeepalive,
          },
        ],
      });
      for (const r of t.routes) {
        await this.mod.addRoute(r); // IPv4 only: the API rejects IPv6 routes
      }
      await this.mod.start();
      this.emit(await this.status());
    } catch (e) {
      this.emit('error', e instanceof Error ? e.message : String(e));
      throw e;
    }
  }

  async disconnect() {
    this.emit('disconnecting');
    await this.mod.stop();
    this.emit('disconnected');
  }

  async status() {
    return toStatus(await this.mod.getStatus());
  }

  subscribe(cb: (s: VpnStatus, detail?: string) => void) {
    this.listeners.add(cb);
    return () => {
      this.listeners.delete(cb);
    };
  }
}

export function systemEngine(): VpnEngine {
  const mod = findModule();
  return mod ? new SystemEngine(mod) : new UnavailableEngine(NOT_AVAILABLE);
}
