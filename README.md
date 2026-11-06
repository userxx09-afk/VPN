# TapperVPN

A mesh VPN where any capable device can be the **gateway** (exit node), and you
choose **which apps** (Windows, Android) or **which devices and sites** (routers)
send their traffic through it. It runs on your own control server and updates
itself from this repository's GitHub Releases.

Everything needed to build and release TapperVPN is in this repo:

| Folder | What it is | Built from |
|---|---|---|
| `tappervpn/` | Client: daemon `tappervpnd`, CLI `tappervpn`, our updater (`updater/`, `cmd/tappervpn-updater`, `cmd/tappervpn-release`) | Tailscale (BSD-3) |
| `headscale/` | Control server | Headscale (BSD-3) |
| `tappervpn-android/` | Android and Fire TV app (`com.tappervpn.ipn`) | tailscale-android (BSD-3) |
| `glinet/` | Package for GL.iNet / OpenWrt routers | ours |
| `server/` | Server config, policy, `install.sh` for the Ubuntu server | ours |
| `scripts/` | Build, install, release and rebrand scripts | ours |
| `docs/` | Design notes, [upstream updates](docs/upstream.md) | ours |

The client and app are renamed forks of Tailscale: the Go module is
`tappervpn.com`, binaries and services are TapperVPN, and the Android package
is `com.tappervpn.ipn`. "Tailscale" still appears in the copyright notices
(the license requires them) and in the addresses of Tailscale's public relay
(DERP) servers, which TapperVPN uses. See [docs/upstream.md](docs/upstream.md).

## What is not in the repo

| What | Where | Why |
|---|---|---|
| Server database and keys (`server/data/`, `/var/lib/headscale/` on the server) | Your private backup | Whoever has them controls the network |
| Release signing key | `%USERPROFILE%\.tappervpn\release.key` | Whoever has it can push updates to every device |
| Android signing key | `%USERPROFILE%\.android\debug.keystore` | Android only accepts updates signed with the same key |
| Toolchains: Go, JDK 17, Android SDK/NDK | Download (see below) | Several GB |

**Back up the two signing keys.** If the release key is lost, devices refuse
new updates and must be reinstalled by hand. If the Android key is lost,
the app must be uninstalled and reinstalled.

## Prerequisites (Windows build PC)

- Go 1.27.2+ (https://go.dev/dl/), Git, Python 3.
- Android: JDK 17 (`winget install Microsoft.OpenJDK.17`), Android SDK with NDK
  23.1.7779620 in `toolchain\android-sdk`, and a Go with
  `patches\go-android-time32.patch` applied in `toolchain\go-android`
  (Fire TV needs it; see the top of `scripts\build-android.ps1`).
- `bin\wintun.dll` (WireGuard's tunnel driver for Windows) is downloaded and
  checked by `scripts\release.ps1`.

## Building

```bash
cd tappervpn && go build -o ../bin/ ./cmd/tappervpnd ./cmd/tappervpn ./cmd/tappervpn-updater
cd ../headscale && go build -o ../bin/ ./cmd/headscale
```

- Android: `scripts\build-android.ps1` (output `bin\tappervpn-debug.apk`).
- Routers: `python glinet/build.py` (see [glinet/README.md](glinet/README.md)).
- Everything, signed, for a release: `scripts\release.ps1`.

## Releases and auto-update

The version lives in `VERSION`. To release: bump it, commit, then run
`scripts\release.ps1 -Publish` (with `$env:GITHUB_TOKEN` set). It builds every
platform, writes `SHA256SUMS`, signs it with the release key, and publishes a
GitHub release with these files:

| File | Installed by |
|---|---|
| `tappervpn-windows-amd64.zip` | Windows: `tappervpn-updater.exe`, daily scheduled task |
| `tappervpn-server-linux-amd64.tar.gz` | Server: `tappervpn-updater`, daily systemd timer |
| `tappervpn-glinet-<cpu>.ipk` | Routers: `/usr/libexec/tappervpn/update`, daily cron |
| `tappervpn-android.apk` | Android: daily background check, then asks you to tap Install |
| `SHA256SUMS`, `SHA256SUMS.sig` | Checked by all of the above |

Each device checks the signature against the public key built into it
(`tappervpn/updater/release.pub`) and the file's hash before installing, so a
tampered download or a release not signed by you is refused. The server
updater backs up the database first and rolls back if Headscale doesn't come
back up. Updates are only offered when the release version is newer than the
installed one.

## Installing

- **Windows**: `scripts\install-windows.ps1` (Administrator) installs to
  `C:\Program Files\TapperVPN`, adds the `TapperVPN` service and the
  `TapperVPN Update` task. Then sign in once with `tappervpn up --login-server=... --auth-key=...`.
- **Server**: copy and unpack the server tarball on the server, then
  `sudo sh install.sh .`.
- **Routers**: see [glinet/README.md](glinet/README.md).
- **Android / Fire TV**: install `tappervpn-android.apk`. The first update asks
  you to allow TapperVPN to install apps.

## Running the dev mesh (Windows)

| Step | Command | Admin? |
|---|---|---|
| 1. Control server | `scripts\start-server.ps1` | No |
| 2. Node daemon (leave running) | `scripts\start-node.ps1` (optional: `-Policy examples\approuting-chrome-only.json -LogFlows` for per-app routing) | Yes |
| 3. Join the mesh | `scripts\join.ps1` (add `-Gateway` to advertise as gateway) | Yes |

- Server config: `server/config.yaml` (listens on `0.0.0.0:8080`; devices find it on the local network or at `https://vp.tapper.co`).
  Data paths in it must be **relative** (to the config's folder): Headscale treats `C:/...` as relative.
- Join key: `server/data/preauthkey.json`. Make a new one with
  `bin\headscale.exe -c server\config.yaml preauthkeys create --user 1 --reusable --expiration 72h`.
- After a node advertises itself as a gateway, approve it on the server:
  `bin\headscale.exe -c server\config.yaml nodes list-routes`, then `nodes approve-routes`.
- Use a gateway from another node: `bin\tappervpn.exe set --exit-node=<gateway-name>`.

## Where our changes are

| Feature | Code |
|---|---|
| Per-app routing (Windows) | `tappervpn/feature/approuting/`, design in [docs/per-app-routing.md](docs/per-app-routing.md) |
| Per-app exit nodes (Android) | `tappervpn-android/android/src/main/java/com/tappervpn/ipn/AppExitNodes.kt` |
| Per-app policy in the control server | `headscale/hscontrol/policy/v2/` (nodeAttrs `app`) |
| Auto-update | `tappervpn/updater/`, `tappervpn/cmd/tappervpn-updater/`, `tappervpn-android/.../AppUpdater.kt`, `glinet/files/usr/libexec/tappervpn/update` |
| Routers | `glinet/` |
| Removed from upstream | Tailscale's own package updater (`tappervpn/clientupdate`, disabled), Headscale's tailsql debug UI, the derper's setec secrets client |

## Roadmap

1. **Own UI**: Windows tray app for choosing the gateway and app list.
2. **macOS**: `NETransparentProxyProvider` system extension.
3. **iOS**: full-tunnel or domain-based only. Per-app VPN needs MDM; iOS cannot be a gateway.
4. **Logo in the web client**: its icons come from Tailscale's prebuilt web UI package; building our own web UI would let it use the TapperVPN logo too.

## Known quirks

- On Windows, many headscale tests report "TempDir RemoveAll cleanup ... being
  used by another process": SQLite still holds the test database open. The
  tests themselves pass; run them on Linux for a clean result.

## Production server (Ubuntu)

The control server runs on the Ubuntu 24.04 VM `frank` (now 10.0.0.156, ssh alias `tapper`).

| What | Where on the server |
|---|---|
| Binaries | `/usr/local/bin/headscale`, `tappervpn`, `tappervpnd`, `tappervpn-updater` |
| Config / policy | `/etc/headscale/config.yaml`, `/etc/headscale/policy.hujson` |
| Database + noise key | `/var/lib/headscale/` (updater backups: `db.sqlite.pre-update-*`) |
| Services | `systemctl status headscale`, `systemctl list-timers tappervpn-update.timer` |
| Admin CLI | `headscale -c /etc/headscale/config.yaml nodes list`, run as the `headscale` user |

Clients use `https://vp.tapper.co` and look for the server on the local network first (see docs/operations.md).
