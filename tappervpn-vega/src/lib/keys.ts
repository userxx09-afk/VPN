// WireGuard keys for this TV. The private key is made here and never leaves
// the device; only the public key is sent to the server when pairing.

import nacl from 'tweetnacl';

export interface KeyPair {
  privateKey: string; // base64, 32 bytes
  publicKey: string; // base64, 32 bytes
}

const B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

export function toBase64(bytes: Uint8Array): string {
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | ((bytes[i + 1] ?? 0) << 8) | (bytes[i + 2] ?? 0);
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63];
    out += i + 1 < bytes.length ? B64[(n >> 6) & 63] : '=';
    out += i + 2 < bytes.length ? B64[n & 63] : '=';
  }
  return out;
}

export function fromBase64(s: string): Uint8Array {
  const clean = s.trim().replace(/=+$/, '');
  if (!/^[A-Za-z0-9+/]*$/.test(clean)) {
    throw new Error('not base64');
  }
  const out: number[] = [];
  let buf = 0;
  let bits = 0;
  for (const c of clean) {
    buf = (buf << 6) | B64.indexOf(c);
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push((buf >> bits) & 255);
    }
  }
  return Uint8Array.from(out);
}

/** WireGuard's private key clamping (Curve25519). */
export function clamp(k: Uint8Array): Uint8Array {
  const out = Uint8Array.from(k);
  out[0] &= 248;
  out[31] = (out[31] & 127) | 64;
  return out;
}

export function publicKeyFor(privateKey: string): string {
  const priv = fromBase64(privateKey);
  if (priv.length !== 32) {
    throw new Error('a WireGuard key is 32 bytes');
  }
  return toBase64(nacl.scalarMult.base(priv));
}

/**
 * Makes a key pair from 32 random bytes. On the TV they come from the
 * system's secure random source (see storage.ts); tests pass their own.
 */
export function keyPairFrom(random: ArrayLike<number>): KeyPair {
  if (random.length !== 32) {
    throw new Error('need 32 random bytes');
  }
  const priv = clamp(Uint8Array.from(random));
  const privateKey = toBase64(priv);
  return {privateKey, publicKey: publicKeyFor(privateKey)};
}

export function isWireGuardKey(s: unknown): s is string {
  if (typeof s !== 'string' || s.length !== 44) {
    return false;
  }
  try {
    return fromBase64(s).length === 32;
  } catch {
    return false;
  }
}
