// Remote-friendly building blocks: large targets, a clear focus ring, and
// nothing that needs a pointer. Everything focusable is a Pressable, which
// the Fire TV remote's D-pad moves between and Select presses.

import React, {useState} from 'react';
import {Pressable, StyleSheet, Text, View, type ViewStyle} from 'react-native';

export const colors = {
  bg: '#0F1420',
  card: '#1A2133',
  cardFocus: '#24304A',
  text: '#F2F4F8',
  muted: '#9AA4B8',
  accent: '#4B70CC', // TapperVPN blue, as in the Android app
  green: '#2FB36B',
  amber: '#E0A526',
  red: '#E05252',
  ring: '#FFFFFF',
};

export function FocusButton(props: {
  label: string;
  onPress: () => void;
  primary?: boolean;
  danger?: boolean;
  disabled?: boolean;
  preferredFocus?: boolean;
  style?: ViewStyle;
  hint?: string;
}) {
  const [focused, setFocused] = useState(false);
  const bg = props.primary ? colors.accent : props.danger ? '#5A2630' : colors.card;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={props.label}
      accessibilityHint={props.hint}
      disabled={props.disabled}
      hasTVPreferredFocus={props.preferredFocus}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      onPress={props.onPress}
      style={[
        styles.button,
        {backgroundColor: focused && !props.primary ? colors.cardFocus : bg},
        focused && styles.focused,
        props.disabled && styles.disabled,
        props.style,
      ]}>
      <Text style={[styles.buttonText, props.primary && styles.buttonTextPrimary]}>
        {props.label}
      </Text>
      {props.hint ? <Text style={styles.hint}>{props.hint}</Text> : null}
    </Pressable>
  );
}

export function Card(props: {children: React.ReactNode; style?: ViewStyle}) {
  return <View style={[styles.card, props.style]}>{props.children}</View>;
}

export function Row(props: {label: string; value: string}) {
  return (
    <View style={styles.row}>
      <Text style={styles.rowLabel}>{props.label}</Text>
      <Text style={styles.rowValue} numberOfLines={1}>
        {props.value}
      </Text>
    </View>
  );
}

export function Title(props: {children: React.ReactNode}) {
  return <Text style={styles.title}>{props.children}</Text>;
}

export function Body(props: {children: React.ReactNode; muted?: boolean}) {
  return <Text style={[styles.body, props.muted && {color: colors.muted}]}>{props.children}</Text>;
}

export const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: colors.bg,
    // TV safe area: keep content away from the edges of the picture.
    paddingHorizontal: 96,
    paddingVertical: 54,
  },
  title: {color: colors.text, fontSize: 44, fontWeight: '700', marginBottom: 18},
  body: {color: colors.text, fontSize: 26, lineHeight: 36},
  button: {
    minHeight: 76,
    minWidth: 320,
    paddingHorizontal: 36,
    borderRadius: 16,
    justifyContent: 'center',
    alignItems: 'center',
    borderWidth: 4,
    borderColor: 'transparent',
    marginVertical: 10,
  },
  focused: {borderColor: colors.ring, transform: [{scale: 1.05}]},
  disabled: {opacity: 0.45},
  buttonText: {color: colors.text, fontSize: 28, fontWeight: '600'},
  buttonTextPrimary: {color: '#FFFFFF'},
  hint: {color: colors.muted, fontSize: 20, marginTop: 4},
  card: {backgroundColor: colors.card, borderRadius: 20, padding: 32, marginVertical: 12},
  row: {flexDirection: 'row', justifyContent: 'space-between', paddingVertical: 8},
  rowLabel: {color: colors.muted, fontSize: 24},
  rowValue: {color: colors.text, fontSize: 24, fontWeight: '600', marginLeft: 24, flexShrink: 1},
});
