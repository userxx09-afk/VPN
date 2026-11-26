// Entry point. The component is registered before anything else loads, so a
// module that fails to load shows its error on the TV instead of the app
// closing straight away ("... has not been registered").

import React from 'react';
import {AppRegistry, Text, View} from 'react-native';
import {name as appName} from './app.json';

function Failed({error}) {
  return (
    <View style={{flex: 1, backgroundColor: '#0F1420', padding: 96, justifyContent: 'center'}}>
      <Text style={{color: '#F2F4F8', fontSize: 44, fontWeight: '700', marginBottom: 18}}>
        TapperVPN couldn't load
      </Text>
      <Text style={{color: '#9AA4B8', fontSize: 26}}>{String(error && (error.stack || error))}</Text>
    </View>
  );
}

function Root() {
  let App;
  let ErrorBoundary;
  try {
    App = require('./src/App').App;
    ErrorBoundary = require('./src/ErrorBoundary').ErrorBoundary;
  } catch (e) {
    console.error('[TapperVPN] load failed:', e);
    return <Failed error={e} />;
  }
  return (
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  );
}

// The Vega runtime launches the component by its app key; register the
// names it may use for com.tappervpn.vega.main.
for (const key of new Set([appName, 'tappervpn', 'tappervpn-vega', 'com.tappervpn.vega', 'com.tappervpn.vega.main', 'main'])) {
  AppRegistry.registerComponent(key, () => Root);
}
