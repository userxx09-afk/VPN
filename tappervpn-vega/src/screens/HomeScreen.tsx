// The main screen: one big Connect button with the status beside it, the
// route choice, and Settings. The remote lands on Connect.

import React from 'react';
import {Image, StyleSheet, Text, View} from 'react-native';

import {Body, Card, colors, FocusButton, Row, styles} from '../components/ui';
import type {BridgeConfig} from '../lib/pairing';
import type {RouteMode} from '../lib/tunnel';
import type {VpnStatus} from '../vpn/VpnEngine';

const STATUS: Record<VpnStatus, {text: string; color: string}> = {
  connected: {text: 'Connected', color: colors.green},
  connecting: {text: 'Connecting…', color: colors.amber},
  disconnecting: {text: 'Disconnecting…', color: colors.amber},
  disconnected: {text: 'Not connected', color: colors.muted},
  error: {text: "Couldn't connect", color: colors.red},
};

export function HomeScreen(props: {
  config: BridgeConfig;
  server: string;
  status: VpnStatus;
  detail: string | null;
  unavailable: string | null;
  mode: RouteMode;
  onToggle: () => void;
  onMode: () => void;
  onSettings: () => void;
}) {
  const s = STATUS[props.status];
  const busy = props.status === 'connecting' || props.status === 'disconnecting';
  const on = props.status === 'connected';

  return (
    <View style={styles.screen}>
      <View style={local.header}>
        <Image source={require('../../assets/image/icon.png')} style={local.logo} />
        <Text style={local.brand}>TapperVPN</Text>
      </View>

      <View style={local.main}>
        <View style={local.left}>
          <View style={[local.dot, {backgroundColor: s.color}]} />
          <Text style={[local.status, {color: s.color}]} accessibilityLiveRegion="polite">
            {s.text}
          </Text>
          {props.detail ? <Body muted>{props.detail}</Body> : null}

          <FocusButton
            label={on ? 'Disconnect' : 'Connect'}
            primary={!on}
            preferredFocus
            disabled={busy || (!!props.unavailable && !on)}
            onPress={props.onToggle}
            style={local.big}
          />
          <FocusButton
            label={props.mode === 'all' ? 'Traffic: all apps' : 'Traffic: only my devices'}
            hint={
              props.mode === 'all'
                ? 'Everything goes through TapperVPN'
                : 'Only your TapperVPN devices go through it'
            }
            onPress={props.onMode}
            style={local.big}
          />
          <FocusButton label="Settings" onPress={props.onSettings} style={local.big} />
        </View>

        <View style={local.right}>
          {props.unavailable ? (
            <Card style={{borderColor: colors.amber, borderWidth: 2}}>
              <Body>{props.unavailable}</Body>
            </Card>
          ) : null}
          <Card>
            <Row label="This TV" value={props.config.name} />
            <Row label="VPN address" value={props.config.address.replace(/\/32$/, '')} />
            <Row label="Server" value={props.server.replace(/^https?:\/\//, '')} />
            <Row label="WireGuard" value={props.config.peer.endpoint} />
          </Card>
        </View>
      </View>
    </View>
  );
}

const local = StyleSheet.create({
  header: {flexDirection: 'row', alignItems: 'center'},
  logo: {width: 64, height: 64, marginRight: 20},
  brand: {color: colors.text, fontSize: 40, fontWeight: '700'},
  main: {flex: 1, flexDirection: 'row', marginTop: 36},
  left: {flex: 1, marginRight: 48},
  right: {flex: 1},
  dot: {width: 28, height: 28, borderRadius: 14, marginBottom: 12},
  status: {fontSize: 56, fontWeight: '800', marginBottom: 24},
  big: {alignSelf: 'stretch'},
});
