#!/bin/bash
# Builds the TapperVPN iOS/iPadOS app on a Mac (Xcode 16+, Go, XcodeGen).
#
#   ./build.sh                 Go library + Xcode project, then an unsigned
#                              device build: build/TapperVPN-unsigned.ipa
#   ./build.sh project         Go library + Xcode project only; then open
#                              TapperVPN.xcodeproj, pick your team, and run
#   ./build.sh testflight      signed archive uploaded to TestFlight; needs
#                              APPLE_TEAM_ID and an App Store Connect API key
#                              (ASC_KEY_PATH, ASC_KEY_ID, ASC_ISSUER_ID)
#
# BUILD_NUMBER (default: minutes since 2026-01-01) must go up with every
# TestFlight upload.
set -euo pipefail
cd "$(dirname "$0")"
mode="${1:-unsigned}"

version="$(tr -d '[:space:]' < ../VERSION)"
build="${BUILD_NUMBER:-$(( ($(date +%s) - 1767225600) / 60 ))}"
tags="$(tr -d '[:space:]' < build-tags.txt)"
engine="$(tr -d '[:space:]' < ../tappervpn/VERSION.txt)"
commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
ldflags="-s -w -X tappervpn.com/updater.Version=$version -X tappervpn.com/version.longStamp=$engine-t${commit::9} -X tappervpn.com/version.shortStamp=$engine -X tappervpn.com/version.gitCommitStamp=$commit"

# 1. The Go client, as a static xcframework for devices and the simulator.
if [ -n "${REUSE_FRAMEWORK:-}" ] && [ -d Frameworks/Libtappervpn.xcframework ]; then
  echo "Reusing Frameworks/Libtappervpn.xcframework"
else
export PATH="$(go env GOPATH)/bin:$PATH"   # gomobile runs gobind from PATH
go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
mkdir -p Frameworks
rm -rf Frameworks/Libtappervpn.xcframework
gomobile bind -target ios,iossimulator -iosversion 16.0 -tags "$tags" -trimpath \
  -ldflags "$ldflags" -o Frameworks/Libtappervpn.xcframework ./libtappervpn
fi

# 2. The Xcode project.
xcodegen generate
[ "$mode" = project ] && { echo "Open TapperVPN.xcodeproj"; exit 0; }

common=(-project TapperVPN.xcodeproj -scheme TapperVPN -configuration Release
  MARKETING_VERSION="$version" CURRENT_PROJECT_VERSION="$build")

case "$mode" in
unsigned)
  xcodebuild "${common[@]}" -destination 'generic/platform=iOS' -derivedDataPath build/dd \
    CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY="" build
  rm -rf build/Payload && mkdir -p build/Payload
  cp -R build/dd/Build/Products/Release-iphoneos/TapperVPN.app build/Payload/
  (cd build && rm -f TapperVPN-unsigned.ipa && zip -qry TapperVPN-unsigned.ipa Payload)
  echo "build/TapperVPN-unsigned.ipa (version $version, build $build)"
  ;;
testflight)
  : "${APPLE_TEAM_ID:?}" "${ASC_KEY_PATH:?}" "${ASC_KEY_ID:?}" "${ASC_ISSUER_ID:?}"
  auth=(-allowProvisioningUpdates -authenticationKeyPath "$ASC_KEY_PATH"
    -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID")
  xcodebuild "${common[@]}" "${auth[@]}" -destination 'generic/platform=iOS' \
    -archivePath build/TapperVPN.xcarchive DEVELOPMENT_TEAM="$APPLE_TEAM_ID" archive
  sed "s/TEAM_ID/$APPLE_TEAM_ID/" ExportOptions.plist > build/ExportOptions.plist
  xcodebuild -exportArchive "${auth[@]}" -archivePath build/TapperVPN.xcarchive \
    -exportOptionsPlist build/ExportOptions.plist -exportPath build/export
  echo "Uploaded version $version (build $build) to App Store Connect / TestFlight"
  ;;
*)
  echo "unknown mode $mode" >&2; exit 2 ;;
esac
