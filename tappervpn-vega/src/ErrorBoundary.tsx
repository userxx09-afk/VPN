// Shows what went wrong instead of a blank screen if a screen crashes.

import React from 'react';
import {Text, View} from 'react-native';

import {colors, styles} from './components/ui';

export class ErrorBoundary extends React.Component<{children: React.ReactNode}, {error: string | null}> {
  state = {error: null as string | null};

  static getDerivedStateFromError(e: unknown) {
    return {error: e instanceof Error ? `${e.name}: ${e.message}` : String(e)};
  }

  componentDidCatch(e: unknown) {
    console.error('[TapperVPN] crashed:', e);
  }

  render() {
    if (this.state.error) {
      return (
        <View style={[styles.screen, {justifyContent: 'center'}]}>
          <Text style={styles.title}>TapperVPN hit an error</Text>
          <Text style={[styles.body, {color: colors.muted}]}>{this.state.error}</Text>
        </View>
      );
    }
    return this.props.children;
  }
}
