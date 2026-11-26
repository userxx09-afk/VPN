// What the app keeps on the TV: its key pair, the server, the settings the
// server sent, and the route mode. AsyncStorage is private to this app.

import AsyncStorage from '@amazon-devices/react-native-async-storage__async-storage';
import {Crypto} from '@amazon-devices/keplercrypto';

import {isWireGuardKey, keyPairFrom, publicKeyFor, type KeyPair} from './keys';
import {DEFAULT_SERVER, parseConfig, type BridgeConfig} from './pairing';
import type {RouteMode} from './tunnel';

const K = {
  privateKey: 'tappervpn.privateKey',
  server: 'tappervpn.server',
  config: 'tappervpn.config',
  mode: 'tappervpn.mode',
  autoConnect: 'tappervpn.autoConnect',
};

export async function loadKeyPair(): Promise<KeyPair> {
  const saved = await AsyncStorage.getItem(K.privateKey);
  if (saved && isWireGuardKey(saved)) {
    return {privateKey: saved, publicKey: publicKeyFor(saved)};
  }
  const random = await new Crypto().getRandom(32);
  const kp = keyPairFrom(random);
  await AsyncStorage.setItem(K.privateKey, kp.privateKey);
  return kp;
}

/** Forget this TV's key and settings; it has to be paired again. */
export async function resetPairing(): Promise<void> {
  await AsyncStorage.multiRemove([K.privateKey, K.config]);
}

export async function loadServer(): Promise<string> {
  return (await AsyncStorage.getItem(K.server)) || DEFAULT_SERVER;
}

export async function saveServer(s: string): Promise<void> {
  await AsyncStorage.setItem(K.server, s);
}

export async function loadConfig(): Promise<BridgeConfig | null> {
  const raw = await AsyncStorage.getItem(K.config);
  if (!raw) {
    return null;
  }
  try {
    return parseConfig(JSON.parse(raw));
  } catch {
    return null;
  }
}

export async function saveConfig(c: BridgeConfig): Promise<void> {
  await AsyncStorage.setItem(K.config, JSON.stringify(c));
}

export async function loadMode(): Promise<RouteMode> {
  return (await AsyncStorage.getItem(K.mode)) === 'mesh' ? 'mesh' : 'all';
}

export async function saveMode(m: RouteMode): Promise<void> {
  await AsyncStorage.setItem(K.mode, m);
}

export async function loadAutoConnect(): Promise<boolean> {
  return (await AsyncStorage.getItem(K.autoConnect)) !== 'false';
}

export async function saveAutoConnect(v: boolean): Promise<void> {
  await AsyncStorage.setItem(K.autoConnect, v ? 'true' : 'false');
}
