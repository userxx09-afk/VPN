# TapperVPN for iPad (and iPhone)

The iOS/iPadOS app: a SwiftUI app plus a Network Extension (packet tunnel)
that runs the same Go client as the other platforms.

| Path | What it is |
|---|---|
| `libtappervpn/` | Go client for iOS, bound to Swift with gomobile (`Libtappervpn.xcframework`). Runs inside the packet tunnel. |
| `PacketTunnel/` | `NEPacketTunnelProvider`: starts the Go client, applies the network settings it asks for, forwards the app's requests to it |
| `App/` | The SwiftUI app: sign in, connect, exit node, devices, settings, logs |
| `Shared/` | App Group settings used by both |
| `project.yml` | XcodeGen spec; `xcodegen generate` writes `TapperVPN.xcodeproj` (not in git) |
| `build.sh` | Builds everything on a Mac (see the top of the file) |

## What it does

Same as the Windows app, laid out for the iPad (sidebar; it becomes a stack
on iPhone and in Slide Over):

- **Sign in**: server (`https://vp.tapper.co`), a device name, and an optional
  auth key. Without a key, the server's approval page opens in Safari and the
  app carries on once the device is approved.
- **Home**: VPN on/off, this device's VPN address and DNS name (tap to copy),
  current exit node, warnings, admin page.
- **Exit node**: None or any device offering itself, plus "Allow local
  network access".
- **Devices**: every device, online first, searchable, tap the address to copy.
- **Settings**: server, device name, sign out, logs (shareable), version.

The exit-node failover order set on the dashboard works as on the other
platforms (it's in the shared client code).

Not on iOS: per-app routing (needs MDM), being an exit node or subnet router
(iOS doesn't allow it), remote control (the screen of other apps can't be
captured or controlled), Taildrop. They're left out of the build
(`build-tags.txt`) to stay under iOS's 50 MB memory limit for VPN extensions.

## How it works

1. The app turns the VPN on with `NETunnelProviderManager`; iOS starts the
   `PacketTunnel` extension.
2. The extension applies placeholder settings (so iOS creates the `utun`
   interface), then calls `LibtappervpnStart`. Go finds the `utun` socket
   among its open files and runs the normal TapperVPN engine on it.
3. When the engine's addresses, routes or DNS change, Go calls back
   `applyNetworkSettings` with JSON; Swift turns it into
   `NEPacketTunnelNetworkSettings`. DNS is split: only tailnet names go to
   MagicDNS, unless an exit node is in use.
4. The app sends JSON requests (`status`, `signIn`, `setExitNode`, ...;
   see `libtappervpn/api.go`) with `sendProviderMessage` and redraws from the
   model in each reply. While the VPN is on, it asks for `status` every 2 s.

State (`tappervpnd.state`) and the log live in the App Group container,
so they survive app updates.

## Building

There's no Mac needed for checking it compiles: GitHub's `iOS` workflow
(`.github/workflows/ios.yml`) builds it on a Mac runner for every change and
keeps an unsigned `.ipa` as an artifact. iOS only installs signed apps,
though, so to put it on an iPad you need an **Apple Developer Program**
membership ($99/year; a free account can't sign VPN extensions).

### Installing on your iPads (TestFlight)

One-time setup:

1. Join the Apple Developer Program as an individual or organization.
2. In [App Store Connect](https://appstoreconnect.apple.com) > Apps, add a new
   iOS app with bundle ID `com.tappervpn.ipn` (register it under
   Certificates, Identifiers & Profiles > Identifiers first, with the
   **Network Extensions** and **App Groups** capabilities; also register
   `com.tappervpn.ipn.network-extension` with the same two, and the App Group
   `group.com.tappervpn.ipn`). If those IDs are taken, change `APP_BUNDLE_ID`
   and `APP_GROUP` in `project.yml`.
3. App Store Connect > Users and Access > Integrations > App Store Connect
   API: create a team key with the **Admin** role (needed for Xcode's cloud
   signing). Download the `.p8` (only possible once).
4. Add these repository secrets (Settings > Secrets and variables > Actions),
   and keep copies in the private store with the other keys:

   | Secret | Value |
   |---|---|
   | `APPLE_TEAM_ID` | Your 10-character Team ID (Membership details) |
   | `ASC_KEY_ID` | The API key's Key ID |
   | `ASC_ISSUER_ID` | The Issuer ID shown above the keys |
   | `ASC_KEY_P8` | The whole text of the `.p8` file |

Then every release (a push to `main` that changes `VERSION`) also uploads
the iOS build to TestFlight, or run Actions > iOS > Run workflow with
"testflight" ticked. In App Store Connect > TestFlight, add yourself and
anyone else as testers; they install the **TestFlight** app on the iPad and
get each new build there (TestFlight builds expire after 90 days, so a
release at least every 3 months keeps them working).

iOS apps can't update themselves the way the Android and Windows apps do;
TestFlight (or the App Store) is the update channel.

### On a Mac

Xcode 16+, Go (see `go.mod`), `brew install xcodegen`:

```bash
./build.sh project      # then open TapperVPN.xcodeproj, set your team, run on an iPad
./build.sh              # unsigned device build: build/TapperVPN-unsigned.ipa
```

VPN extensions don't run in the iOS Simulator; test on a real device.
