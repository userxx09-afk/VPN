import {clamp, fromBase64, isWireGuardKey, keyPairFrom, publicKeyFor, toBase64} from '../keys';
import {normalizeServer, PairingClient, PairingError, parseConfig, type Fetch} from '../pairing';
import {tunnelSettings, wgQuickConfig} from '../tunnel';

const serverKey = toBase64(new Uint8Array(32).fill(9));

const goodConfig = {
  name: 'Living room TV',
  address: '10.77.0.2/32',
  dns: ['1.1.1.1'],
  mtu: 1280,
  peer: {
    publicKey: serverKey,
    endpoint: 'vp.tapper.co:51821',
    allowedIPs: ['0.0.0.0/0'],
    meshIPs: ['100.64.0.0/10', '10.77.0.0/24'],
    persistentKeepalive: 25,
  },
};

describe('keys', () => {
  test('base64 round trip matches Node', () => {
    for (const n of [0, 1, 2, 3, 31, 32, 33]) {
      const b = Uint8Array.from({length: n}, (_, i) => (i * 37 + 11) & 255);
      expect(toBase64(b)).toBe(Buffer.from(b).toString('base64'));
      expect(Buffer.from(fromBase64(toBase64(b)))).toEqual(Buffer.from(b));
    }
  });

  test('public key is X25519, same as wg pubkey', () => {
    const random = Uint8Array.from({length: 32}, (_, i) => i * 7 + 1);
    const kp = keyPairFrom(random);
    const priv = clamp(random);
    expect(kp.privateKey).toBe(Buffer.from(priv).toString('base64'));
    // RFC 7748 section 6.1 test vector (Alice).
    const alicePriv = Buffer.from('77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a', 'hex');
    const alicePub = '8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a';
    expect(Buffer.from(fromBase64(publicKeyFor(alicePriv.toString('base64')))).toString('hex')).toBe(alicePub);
    expect(isWireGuardKey(kp.publicKey)).toBe(true);
    expect(isWireGuardKey('short')).toBe(false);
  });
});

describe('pairing', () => {
  test('normalizeServer', () => {
    expect(normalizeServer('vp.tapper.co')).toBe('https://vp.tapper.co');
    expect(normalizeServer(' https://vp.tapper.co/ ')).toBe('https://vp.tapper.co');
    expect(normalizeServer('http://192.168.1.5:8080')).toBe('http://192.168.1.5:8080');
    expect(normalizeServer('')).toBe('https://vp.tapper.co');
  });

  test('parseConfig rejects broken settings', () => {
    expect(parseConfig(goodConfig).peer.endpoint).toBe('vp.tapper.co:51821');
    expect(() => parseConfig({...goodConfig, address: ''})).toThrow(PairingError);
    expect(() => parseConfig({...goodConfig, peer: {...goodConfig.peer, publicKey: 'x'}})).toThrow(PairingError);
    expect(() => parseConfig({...goodConfig, peer: {...goodConfig.peer, endpoint: 'nohost'}})).toThrow(PairingError);
    const noRoutes = parseConfig({...goodConfig, peer: {...goodConfig.peer, allowedIPs: ['::/0']}});
    expect(noRoutes.peer.allowedIPs).toEqual(['0.0.0.0/0']);
  });

  function fakeServer(handler: (url: string, init?: {method?: string; body?: string}) => [number, unknown]): {fetch: Fetch; calls: string[]} {
    const calls: string[] = [];
    return {
      calls,
      fetch: async (url, init) => {
        calls.push(`${init?.method ?? 'GET'} ${url}`);
        const [status, body] = handler(url, init);
        return {
          status,
          json: async () => {
            if (typeof body === 'string') {
              throw new Error('not json');
            }
            return body;
          },
        };
      },
    };
  }

  test('pair, wait, approved', async () => {
    let approved = false;
    const s = fakeServer((url, init) => {
      if (init?.method === 'POST') {
        expect(JSON.parse(init.body!)).toEqual({publicKey: serverKey, name: 'Fire TV'});
        return [200, {status: 'pending', code: '123456', expiresIn: 900}];
      }
      expect(url).toContain('key=' + encodeURIComponent(serverKey));
      return approved ? [200, {status: 'approved', config: goodConfig}] : [202, {status: 'pending'}];
    });
    const c = new PairingClient('vp.tapper.co', s.fetch);
    expect(await c.start(serverKey, 'Fire TV')).toEqual({status: 'pending', code: '123456', expiresIn: 900});
    expect(await c.state(serverKey)).toEqual({status: 'pending'});
    approved = true;
    const st = await c.state(serverKey);
    expect(st.status).toBe('approved');
    expect(s.calls[0]).toBe('POST https://vp.tapper.co/tappervpn/wg/pair');
  });

  test('errors are explained', async () => {
    const off = new PairingClient('x', fakeServer(() => [404, '404 page not found']).fetch);
    await expect(off.start(serverKey, 'TV')).rejects.toThrow(/wg-bridge install/);
    await expect(off.state(serverKey)).rejects.toThrow(/wg-bridge install/);

    const unknown = new PairingClient('x', fakeServer(() => [404, {status: 'unknown'}]).fetch);
    expect(await unknown.state(serverKey)).toEqual({status: 'unknown'});

    const busy = new PairingClient('x', fakeServer(() => [429, {error: 'busy'}]).fetch);
    await expect(busy.start(serverKey, 'TV')).rejects.toThrow(/15 minutes/);

    const down = new PairingClient('x', async () => {
      throw new Error('ENOTFOUND');
    });
    await expect(down.start(serverKey, 'TV')).rejects.toThrow(/Can't reach https:\/\/x/);
  });
});

describe('tunnel', () => {
  const cfg = parseConfig(goodConfig);
  const priv = toBase64(new Uint8Array(32).fill(1));

  test('all traffic', () => {
    const t = tunnelSettings(cfg, priv, 'all');
    expect(t.routes).toEqual(['0.0.0.0/0']);
    expect(t.dns).toEqual(['1.1.1.1']);
    expect(t.peer.endpointHost).toBe('vp.tapper.co');
    expect(t.peer.endpointPort).toBe(51821);
    expect(wgQuickConfig(t)).toContain('AllowedIPs = 0.0.0.0/0');
    expect(wgQuickConfig(t)).toContain('DNS = 1.1.1.1');
  });

  test('only my devices', () => {
    const t = tunnelSettings(cfg, priv, 'mesh');
    expect(t.routes).toEqual(['100.64.0.0/10', '10.77.0.0/24']);
    expect(t.dns).toEqual([]);
    expect(wgQuickConfig(t)).not.toContain('DNS =');
  });
});
