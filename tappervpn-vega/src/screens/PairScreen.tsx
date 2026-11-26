// First run: the TV asks the server for a pairing code and shows it. The
// admin approves it on the server (sudo wg-bridge approve <code>); this
// screen notices within a few seconds and moves on.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {ActivityIndicator, Image, StyleSheet, Text, View} from 'react-native';

import {Body, Card, colors, FocusButton, styles, Title} from '../components/ui';
import {PairingClient, PairingError, type BridgeConfig} from '../lib/pairing';

const POLL_MS = 3000;

export function PairScreen(props: {
  client: PairingClient;
  server: string;
  publicKey: string;
  onPaired: (c: BridgeConfig) => void;
  onSettings: () => void;
}) {
  const {client, publicKey, onPaired} = props;
  const [code, setCode] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const expires = useRef(0);

  const retry = useCallback(() => {
    setError(null);
    setCode(null);
    setAttempt(a => a + 1);
  }, []);

  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const fail = (e: unknown) => {
      if (!stop) {
        setError(e instanceof PairingError ? e.message : String(e));
      }
    };

    const poll = async () => {
      if (stop) {
        return;
      }
      try {
        const s = await client.state(publicKey);
        if (stop) {
          return;
        }
        if (s.status === 'approved') {
          onPaired(s.config);
          return;
        }
        if (s.status === 'unknown' || Date.now() > expires.current) {
          // The code expired or was rejected: get a new one.
          await begin();
          return;
        }
      } catch (e) {
        fail(e);
        return;
      }
      timer = setTimeout(poll, POLL_MS);
    };

    const begin = async () => {
      try {
        const r = await client.start(publicKey, 'Fire TV');
        if (stop) {
          return;
        }
        if (r.status === 'pending') {
          setCode(r.code);
          expires.current = Date.now() + r.expiresIn * 1000;
        }
        timer = setTimeout(poll, r.status === 'approved' ? 0 : POLL_MS);
      } catch (e) {
        fail(e);
      }
    };

    begin();
    return () => {
      stop = true;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [client, publicKey, onPaired, attempt]);

  return (
    <View style={styles.screen}>
      <View style={local.header}>
        <Image source={require('../../assets/image/icon.png')} style={local.logo} />
        <Title>Set up TapperVPN</Title>
      </View>

      {error ? (
        <Card>
          <Body>{error}</Body>
          <View style={local.buttons}>
            <FocusButton label="Try again" primary preferredFocus onPress={retry} />
            <FocusButton label="Server settings" onPress={props.onSettings} />
          </View>
        </Card>
      ) : code ? (
        <Card>
          <Body muted>Pairing code for this TV</Body>
          <Text style={local.code} accessibilityLabel={`Pairing code ${code.split('').join(' ')}`}>
            {code.slice(0, 3)} {code.slice(3)}
          </Text>
          <Body>On your TapperVPN server, run:</Body>
          <Text style={local.cmd}>sudo wg-bridge approve {code}</Text>
          <View style={local.waiting}>
            <ActivityIndicator color={colors.accent} size="large" />
            <Body muted>  Waiting for approval. This screen moves on by itself.</Body>
          </View>
          <View style={local.buttons}>
            <FocusButton label="Server settings" preferredFocus onPress={props.onSettings} />
          </View>
        </Card>
      ) : (
        <View style={local.waiting}>
          <ActivityIndicator color={colors.accent} size="large" />
          <Body muted>  Contacting {props.server}…</Body>
        </View>
      )}
    </View>
  );
}

const local = StyleSheet.create({
  header: {flexDirection: 'row', alignItems: 'center', marginBottom: 12},
  logo: {width: 72, height: 72, marginRight: 24, marginBottom: 18},
  code: {
    color: colors.text,
    fontSize: 120,
    fontWeight: '800',
    letterSpacing: 12,
    marginVertical: 16,
    fontVariant: ['tabular-nums'],
  },
  cmd: {
    color: colors.accent,
    fontSize: 30,
    fontFamily: 'monospace',
    marginTop: 8,
    marginBottom: 24,
  },
  waiting: {flexDirection: 'row', alignItems: 'center', marginTop: 12},
  buttons: {flexDirection: 'row', marginTop: 24, gap: 24},
});
