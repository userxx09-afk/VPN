// Server address, auto-connect, and pairing again. Back on the remote
// returns to the previous screen.

import React, {useEffect, useState} from 'react';
import {BackHandler, StyleSheet, TextInput, View} from 'react-native';

import {Body, Card, colors, FocusButton, styles, Title} from '../components/ui';
import {normalizeServer} from '../lib/pairing';

export function SettingsScreen(props: {
  server: string;
  paired: boolean;
  autoConnect: boolean;
  publicKey: string;
  onServer: (s: string) => void;
  onAutoConnect: (v: boolean) => void;
  onRepair: () => void;
  onBack: () => void;
}) {
  const [server, setServer] = useState(props.server);
  const [confirmRepair, setConfirmRepair] = useState(false);

  useEffect(() => {
    const sub = BackHandler.addEventListener('hardwareBackPress', () => {
      props.onBack();
      return true;
    });
    return () => sub.remove();
  }, [props]);

  return (
    <View style={styles.screen}>
      <Title>Settings</Title>

      <Card>
        <Body muted>Control server</Body>
        <TextInput
          value={server}
          onChangeText={setServer}
          onSubmitEditing={() => props.onServer(normalizeServer(server))}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
          style={local.input}
          accessibilityLabel="Control server address"
        />
        <FocusButton
          label="Save server"
          disabled={normalizeServer(server) === props.server}
          onPress={() => props.onServer(normalizeServer(server))}
        />
      </Card>

      <View style={local.row}>
        <FocusButton
          label={props.autoConnect ? 'Connect when the app opens: on' : 'Connect when the app opens: off'}
          preferredFocus
          onPress={() => props.onAutoConnect(!props.autoConnect)}
        />
        {props.paired ? (
          confirmRepair ? (
            <FocusButton
              label="Yes, forget this TV's key"
              danger
              hint="You'll need to approve a new code on the server"
              onPress={props.onRepair}
            />
          ) : (
            <FocusButton label="Pair again" onPress={() => setConfirmRepair(true)} />
          )
        ) : null}
        <FocusButton label="Back" onPress={props.onBack} />
      </View>

      <Body muted>This TV's WireGuard key: {props.publicKey}</Body>
    </View>
  );
}

const local = StyleSheet.create({
  input: {
    color: colors.text,
    fontSize: 30,
    backgroundColor: colors.bg,
    borderRadius: 12,
    paddingHorizontal: 20,
    paddingVertical: 14,
    marginVertical: 12,
  },
  row: {flexDirection: 'row', flexWrap: 'wrap', gap: 24, marginVertical: 12},
});
