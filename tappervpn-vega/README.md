# TapperVPN for Fire TV on Vega OS

The app for Fire TVs that run **Vega OS** (Amazon's Linux and React Native
system, for example the Fire TV Stick 4K Select). Fire TVs on Fire OS
(Android) use the normal Android app, `tappervpn-android/`.

## Why it works differently

Vega OS doesn't run Android apps, and it gives apps only one kind of VPN: a
system **WireGuard** tunnel (Amazon's "VPN Manager API": WireGuard only, IPv4
only, one tunnel per device). TapperVPN is built on Tailscale, which has to run
its own engine inside the tunnel, so a Vega TV can't be a full mesh device.

Instead the TV connects with plain WireGuard to a **bridge on the control
server** (`server/wg-bridge.sh`). From there its traffic goes out through the
server (or the exit node the server uses), and it can reach your devices'
VPN addresses (100.x.y.z).

```
Fire TV (Vega) --WireGuard--> control server (tvpn0) --> internet, or mesh / exit node
```

## What the app does

Built for the remote: everything is a large button the D-pad moves between,
with a white focus ring, and Back returns from Settings.

- **Pairing**: the TV makes its own WireGuard key (the private key never
  leaves it), asks the server for a 6-digit code and shows it. You approve it
  on the server; the TV moves on by itself within a few seconds.
- **Connect / Disconnect**, with the status, and connect when the app opens.
- **Traffic**: all apps through TapperVPN, or only traffic to your TapperVPN
  devices.
- **Settings**: the control server (default `https://vp.tapper.co`), and
  pairing again.

## What's still needed: Amazon's VPN API

Amazon hasn't published the VPN Manager API for every app. It isn't in the
[Vega API list](https://developer.amazon.com/docs/vega-api/2.0/vega-apis-overview.html)
or on npm; the VPN apps on Vega so far (NordVPN, IPVanish, Proton, Surfshark,
ExpressVPN) are VPN companies working with Amazon. Until TapperVPN has it, the
app pairs and shows its settings, but says it can't start the VPN yet instead
of pretending to connect.

To get it:

1. An Amazon Developer account (free): https://developer.amazon.com
2. Ask Amazon's Vega developer support for access to the VPN Manager API for
   `com.tappervpn.vega`, and for its package and manifest privilege.
3. Then two files change: `src/vpn/system.ts` (module name and calls) and
   `manifest.toml` (the privilege, marked there). Everything else is ready.

## Server setup (once)

On the control server, after `install.sh` (which installs `wg-bridge`):

```sh
sudo apt install wireguard-tools jq
sudo wg-bridge install            # vp.tapper.co, UDP 51821
```

Forward **UDP 51821** on the router to the server. Then, for each TV:

```sh
sudo wg-bridge list               # codes waiting for approval
sudo wg-bridge approve 123456 "Living room TV"
sudo wg-bridge remove "Living room TV"
sudo wg-bridge dns 192.168.1.1    # DNS for TVs (default 1.1.1.1, 9.9.9.9)
```

TV traffic leaves through whatever this server uses: its own internet
connection, or an exit node set with `tappervpn set --exit-node=<name>` (that
also moves the server's own traffic). The bridge's key and peer list are in
`/etc/tappervpn/wg-bridge/`; back that folder up with the server's database.
A lost bridge key only means pairing the TVs again.

## Building

Needs Node 22 and, for the final `.vpkg`, Amazon's Vega SDK (macOS or Ubuntu;
on Windows use WSL): https://developer.amazon.com/docs/vega/2.0/install-vega-sdk

```sh
cd tappervpn-vega
npm ci
npm run typecheck && npm test
npm run build:app        # needs the Vega SDK; output in build/
```

`npm run bundle` builds the Hermes bundle without the SDK (GitHub's `build`
workflow does this on every push).

Install on a Fire TV in Developer Mode (Settings > My Fire TV > Developer
Options):

```sh
vega devmode login
vega devmode enable-device --code <code shown on the TV>
vega device install-app --packagePath <the .vpkg in build/>
vega device launch-app --appName com.tappervpn.vega.main
```

Retail Vega TVs only install apps from the Amazon Appstore, so for every TV
in the house the app has to be published there (it can be unlisted or
limited to your account). That also needs the VPN access above.

## Files

| File | What |
|---|---|
| `src/App.tsx` | Screens and state |
| `src/screens/` | Pairing, Home, Settings (D-pad UI) |
| `src/lib/keys.ts` | WireGuard key pair (X25519) |
| `src/lib/pairing.ts` | Server pairing API (`headscale/hscontrol/wgbridge.go`) |
| `src/lib/tunnel.ts` | Tunnel settings, routes for "all apps" and "only my devices" |
| `src/vpn/system.ts` | The only code that talks to Vega's VPN API |
| `manifest.toml` | Vega app manifest (`com.tappervpn.vega`) |
