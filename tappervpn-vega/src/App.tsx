// TapperVPN for Fire TV on Vega OS.
//
// The TV pairs with the control server's WireGuard bridge (see
// server/wg-bridge.sh), then turns the system WireGuard tunnel on and off.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {ActivityIndicator, View} from 'react-native';

import {Body, colors, FocusButton, styles, Title} from './components/ui';
import type {KeyPair} from './lib/keys';
import {PairingClient, type BridgeConfig, type Fetch} from './lib/pairing';
import * as store from './lib/storage';
import {tunnelSettings, type RouteMode} from './lib/tunnel';
import {HomeScreen} from './screens/HomeScreen';
import {PairScreen} from './screens/PairScreen';
import {SettingsScreen} from './screens/SettingsScreen';
import {systemEngine} from './vpn/system';
import type {VpnStatus} from './vpn/VpnEngine';

type Screen = 'home' | 'settings';

export function App() {
  const engine = useMemo(() => systemEngine(), []);
  const [keys, setKeys] = useState<KeyPair | null>(null);
  const [server, setServer] = useState<string | null>(null);
  const [config, setConfig] = useState<BridgeConfig | null>(null);
  const [mode, setMode] = useState<RouteMode>('all');
  const [autoConnect, setAutoConnect] = useState(true);
  const [screen, setScreen] = useState<Screen>('home');
  const [status, setStatus] = useState<VpnStatus>('disconnected');
  const [detail, setDetail] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const autoConnected = useRef(false);

  const client = useMemo(
    () => (server ? new PairingClient(server, fetch as unknown as Fetch) : null),
    [server],
  );

  const [fatal, setFatal] = useState<string | null>(null);
  const [startTry, setStartTry] = useState(0);

  // Load what's saved, then follow the tunnel's status.
  useEffect(() => {
    (async () => {
      try {
      const [k, s, c, m, a, why] = await Promise.all([
        store.loadKeyPair(),
        store.loadServer(),
        store.loadConfig(),
        store.loadMode(),
        store.loadAutoConnect(),
        engine.unavailableReason(),
      ]);
      setKeys(k);
      setServer(s);
      setConfig(c);
      setMode(m);
      setAutoConnect(a);
      setUnavailable(why);
      setStatus(await engine.status());
      setFatal(null);
      setReady(true);
      } catch (e) {
        const msg = e instanceof Error ? `${e.message}` : String(e);
        console.error('[TapperVPN] startup failed:', msg);
        setFatal(msg);
      }
    })();
    return engine.subscribe((s, d) => {
      setStatus(s);
      setDetail(d ?? null);
    });
  }, [engine, startTry]);

  // Pick up changes made on the server (DNS, port) and notice removal.
  useEffect(() => {
    if (!client || !keys || !config) {
      return;
    }
    client
      .state(keys.publicKey)
      .then(async st => {
        if (st.status === 'approved') {
          setConfig(st.config);
          await store.saveConfig(st.config);
        } else if (st.status === 'unknown') {
          setDetail('This TV was removed on the server. Choose Settings, then Pair again.');
        }
      })
      .catch(() => {
        // Offline or server down: keep using the saved settings.
      });
    // Only on start and when the server changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, keys]);

  const connect = useCallback(
    async (m: RouteMode) => {
      if (!keys || !config) {
        return;
      }
      setDetail(null);
      try {
        await engine.connect(tunnelSettings(config, keys.privateKey, m));
      } catch (e) {
        setStatus('error');
        setDetail(e instanceof Error ? e.message : String(e));
      }
    },
    [engine, keys, config],
  );

  useEffect(() => {
    if (ready && config && autoConnect && !unavailable && !autoConnected.current) {
      autoConnected.current = true;
      if (status === 'disconnected') {
        connect(mode);
      }
    }
  }, [ready, config, autoConnect, unavailable, status, mode, connect]);

  const toggle = useCallback(async () => {
    if (status === 'connected') {
      await engine.disconnect();
    } else {
      await connect(mode);
    }
  }, [status, engine, connect, mode]);

  const changeMode = useCallback(async () => {
    const m: RouteMode = mode === 'all' ? 'mesh' : 'all';
    setMode(m);
    await store.saveMode(m);
    if (status === 'connected') {
      // Routes are set when the tunnel starts, so restart it.
      await engine.disconnect();
      await connect(m);
    }
  }, [mode, status, engine, connect]);

  const onPaired = useCallback(async (c: BridgeConfig) => {
    await store.saveConfig(c);
    setConfig(c);
    setScreen('home');
  }, []);

  const repair = useCallback(async () => {
    if (status === 'connected') {
      await engine.disconnect();
    }
    await store.resetPairing();
    setConfig(null);
    setKeys(await store.loadKeyPair());
    setScreen('home');
  }, [status, engine]);

  if (fatal) {
    return (
      <View style={[styles.screen, {justifyContent: 'center'}]}>
        <Title>TapperVPN couldn't start</Title>
        <Body>{fatal}</Body>
        <FocusButton label="Try again" primary preferredFocus onPress={() => setStartTry(n => n + 1)} />
      </View>
    );
  }

  if (!ready || !keys || !server || !client) {
    return (
      <View style={[styles.screen, {justifyContent: 'center', alignItems: 'center'}]}>
        <ActivityIndicator color={colors.accent} size="large" />
        <Body muted>Starting TapperVPN…</Body>
      </View>
    );
  }

  if (screen === 'settings') {
    return (
      <SettingsScreen
        server={server}
        paired={!!config}
        autoConnect={autoConnect}
        publicKey={keys.publicKey}
        onServer={async s => {
          await store.saveServer(s);
          setServer(s);
        }}
        onAutoConnect={async v => {
          setAutoConnect(v);
          await store.saveAutoConnect(v);
        }}
        onRepair={repair}
        onBack={() => setScreen('home')}
      />
    );
  }

  if (!config) {
    return (
      <PairScreen
        client={client}
        server={server}
        publicKey={keys.publicKey}
        onPaired={onPaired}
        onSettings={() => setScreen('settings')}
      />
    );
  }

  return (
    <HomeScreen
      config={config}
      server={server}
      status={status}
      detail={detail}
      unavailable={unavailable}
      mode={mode}
      onToggle={toggle}
      onMode={changeMode}
      onSettings={() => setScreen('settings')}
    />
  );
}

