import React from 'react';
import {AppRegistry} from 'react-native';
import {App} from './src/App';
import {ErrorBoundary} from './src/ErrorBoundary';
import {name as appName} from './app.json';

console.log('[TapperVPN] starting');
AppRegistry.registerComponent(appName, () => () => (
  <ErrorBoundary>
    <App />
  </ErrorBoundary>
));
