const path = require('path');
const {getDefaultConfig, mergeConfig} = require('@react-native/metro-config');

module.exports = mergeConfig(getDefaultConfig(__dirname), {
  transformer: {
    // react-native-kepler's package.json points react-native/asset-registry
    // at a file it doesn't ship; use the real one so images bundle.
    assetRegistryPath: path.join(
      path.dirname(require.resolve('@amazon-devices/react-native-kepler/package.json')),
      'Libraries/Image/AssetRegistry.js',
    ),
  },
});
