# Running TapperVPN: setup, moving the server, troubleshooting

How our network is set up, how to sign devices in, how to move the control
server to another machine, and every problem we've hit with its fix. Keep
this up to date when something changes. Secrets (keys, tokens, the server
database) are never in this repo; see "What is not in the repo" in the
[README](../README.md).

## The network

The network was rearranged on 2026-11-05. The PC and tapper are now on
10.0.0.0/24 (gateway 10.0.0.1, Comcast DNS). tapper's eth1 has
10.0.0.156 by DHCP and carries the default route; eth0 (the old eero card,
with the fixed 192.168.5.210) is unplugged. Nobody needs to know tapper's
address any more: devices find it on the local network (next section).

If eth1's NetworkManager connection ("Wired connection 2") ever says
`ipv4.never-default yes`, tapper has no internet and headscale won't start
(it downloads Tailscale's relay map at startup). Fix:
`sudo nmcli connection modify "Wired connection 2" ipv4.never-default no`
then `sudo nmcli connection up "Wired connection 2"`.

## How devices find the control server

Devices (0.2.4+) are set up with one address, **https://vp.tapper.co**.
Every time one connects, it first asks the local network (mDNS, service
`_tappervpn._tcp`) whether a control server is there:

- tapper announces itself with avahi (`/etc/avahi/services/tappervpn.service`,
  installed by `server/install.sh`).
- A device only uses a server that holds the control key built into the app
  (`ServerKey` in `tappervpn/control/localcontrol`), so a stranger's server
  on some other network can't pose as ours. Standby servers must use the
  same `/var/lib/headscale/noise_private.key`.
- If nothing answers, the device uses vp.tapper.co. Switching between the
  two never signs a device out: it's the same server.
- Check from the build PC: `go run ./cmd/localcontrol-probe` in `tappervpn/`.
  From Linux: `avahi-browse -rt _tappervpn._tcp`.

**vp.tapper.co doesn't exist in DNS yet.** Until it does (A record to the
public IP, port forward, HTTPS on tapper), devices only reach the server
at home.

`server_url` in `/etc/headscale/config.yaml` only sets the address shown on
sign-in pages; it's `http://10.0.0.156:8080` for now and becomes
`https://vp.tapper.co` once that works. tapper's own node talks to
`http://127.0.0.1:8080`.

## The server ("tapper")

Ubuntu 24.04 Hyper-V VM `frank`, ssh alias `tapper`, passwordless sudo.

| What | Where |
|---|---|
| Control server | `headscale.service`, listens on `0.0.0.0:8080` (both cards) |
| Sign-in page address | `server_url` in `/etc/headscale/config.yaml` (now `http://10.0.0.156:8080`) |
| Local announcement | avahi, `/etc/avahi/services/tappervpn.service` |
| Database and keys | `/var/lib/headscale/` (`db.sqlite`, `noise_private.key`) |
| Policy | `/etc/headscale/policy.hujson` |
| The server's own VPN node | `tappervpnd.service`, state in `/var/lib/tappervpn/`, node name `tapper`, exit node |
| Auto-update | `tappervpn-update.timer`, daily ~04:00; logs: `journalctl -u tappervpn-update` |
| Public name | `tappervpn-duckdns.timer` (every 5 min), settings in `/etc/tappervpn/duckdns.env` (root only) |
| Backups | `/var/backups/tappervpn-pre-0.2.0-*` (before the first install), `/var/lib/headscale/db.sqlite.pre-update-*` (before each update) |

Admin commands (on the server, with `sudo`):

```bash
headscale nodes list                      # devices, IPs, online
headscale nodes list-routes               # exit nodes / subnet routes
headscale nodes approve-routes -i <id> -r 0.0.0.0/0,::/0
headscale nodes delete -i <id>
headscale auth register --auth-id hskey-authreq-... --user admin   # approve a sign-in
headscale preauthkeys create --user admin --expiration 1h          # key for a headless device
tappervpn status                          # the server's own node
journalctl -u headscale -f                # watch sign-ins live
```

## Devices

| Device | VPN address | Notes |
|---|---|---|
| Build PC (Windows) | 100.64.0.1 | client not running at the moment; also offers itself as an exit node |
| tapper (the server) | 100.64.0.2 | exit node |
| Phone (Android) | 100.64.0.3 | |
| Fire TV Cube | 100.64.0.4 | needs the `armeabi-v7a` build (the release APK has all ABIs) |

### Signing a device in

1. Install the app (Android/Fire TV: `tappervpn-android.apk` from the latest release).
2. Tap **Log in**. Since 0.2.1 the app goes to the built-in server; to use
   another one, pick **Use an alternate server** first (it is remembered).
3. A page (phone) or QR code (Fire TV; scan it with the phone) shows
   `headscale auth register --auth-id hskey-authreq-... --user USERNAME`.
4. On the server: `sudo headscale auth register --auth-id <that id> --user admin`.

Servers and other headless machines: `server/join-exit-node.sh` joins with a
one-time key and approves the exit routes.

### Using tapper as an exit node

On a device: **Exit node → tapper**. All its internet traffic then goes
through tapper and out the home connection.

## When the control server is down

Devices that are already connected keep talking: traffic goes device to
device (or through Tailscale's public relays), not through the control
server. Since 0.2.3 every device also saves its network map to disk (policy
nodeAttr `cache-network-maps`), so a device that restarts during an outage
still reconnects to its peers. What stops working until the server is back:
signing in new devices and changing rules or exit nodes.

Plan for three sites (one exposed primary, two standbys that aren't exposed):
the primary copies its database and keys to the standbys continuously; a
standby is started only when the primary has been unreachable for a while
(manual command first, automatic later); clients get a fallback list of
control server addresses. Only one control server may run at a time.

## Moving the server to another machine

1. On the old server, stop and copy the state:
   ```bash
   sudo systemctl stop headscale tappervpnd
   sudo tar -czf ~/tappervpn-state.tgz /etc/headscale /var/lib/headscale /var/lib/tappervpn /etc/tappervpn /etc/default/tappervpnd
   ```
   Keep that file private: it holds the server's keys.
2. On the new machine (Ubuntu): download `tappervpn-server-linux-amd64.tar.gz`
   from the latest release, unpack it, and run `sudo sh install.sh .`
   (`apt install jq` too, for `join-exit-node.sh`).
3. Stop the new headscale, unpack the state over it with
   `sudo tar -xzf tappervpn-state.tgz -C /`, then
   `sudo chown -R headscale:headscale /var/lib/headscale` and start
   `headscale` and `tappervpnd` again.
4. If the new machine's address differs, update `server_url` in
   `/etc/headscale/config.yaml`, the router's port forward and DHCP
   reservation. Devices keep working without signing in again as long as the
   address they use (best: the DuckDNS name) still reaches the server.
5. If tappervpnd's state wasn't copied, run `sudo sh join-exit-node.sh` to
   join the new machine as a node.

## Releasing

Bump `VERSION`, commit, run `scripts\release.ps1` (builds and signs into
`bin\release\<version>`), then publish. Publishing needs a GitHub token with
"Contents: read and write" on the repo in the user environment variable
`GITHUB_TOKEN` (set it with a hidden prompt; never paste a token in chat or a
file in the repo). Devices pick a release up within a day.

## Problems we've hit, and the fixes

| Symptom | Cause | Fix |
|---|---|---|
| New clients couldn't sign in to the server at all | The rename had changed the control-protocol handshake strings (`Tailscale Control Protocol v`, `tailscale-control-protocol`, `X-Tailscale-Handshake`) | Kept Tailscale's spelling for those; `scripts/rebrand.py` protects them (commit d036e8c) |
| Captive-portal checks against relays failed | `X-Tailscale-Challenge/Response` headers renamed | Protected in `rebrand.py` |
| "Use an alternate server" was ignored; every login went to the built-in address | The built-in address was handed out as the LoginURL policy, which the backend treats as locked | 0.2.1: the app remembers the address picked there and uses it as the policy |
| Log in did nothing after the server's IP changed (.207 → .210) | The address was built into the app; DHCP moved the server | 0.2.1 points at .210; reserve the server's addresses on both routers |
| No device could reach the control server; tapper's own node offline; already-connected devices still worked | The eero moved eth0 again (.210 → .211, 2026-11-05) | Added .210 as a fixed second address on eth0 (see "The network"); reach the server over eth1 192.168.1.182 if eth0 is lost |
| Control server down after the network was rearranged; headscale kept restarting | eth1 was set to never carry the default route (fine while eth0 had it); with eth0 unplugged tapper had no DNS, and headscale exits if it can't download the relay map | Let eth1 carry the default route (see "The network") |
| Devices stuck on an old address every time the network changed | Server address built into the app | 0.2.4: devices look for the server on the local network, then vp.tapper.co |
| Update downloaded, then "App not installed" | A test build installed by hand had a higher versionCode (it came from the build time) than the release | 0.2.2: versionCode comes from `VERSION`, so releases always go up |
| Update not offered for hours | The background check runs once a day | 0.2.2: Settings → Check for updates, and opening the app checks if the last check is 6+ hours old |
| Fire TV install failed: signature mismatch | The old app was signed with a key that was lost | Uninstalled, reinstalled; keep `%USERPROFILE%\.android\debug.keystore` backed up |
| Fire TV app crashed (SIGSYS) | Stock Go uses `futex_time64`, which Fire OS's seccomp kills | Build Android with the patched Go (`patches\go-android-time32.patch`) |
| Fire TV VPN prompt "auto-rejected" | The TV was asleep when the prompt came up | Wake it, choose Try again, then OK |
| Browser warning on the sign-in page | Plain HTTP | Expected on the LAN; HTTPS comes with the public name |
| A fresh Windows clone failed to check out | A headscale test fixture had `>` in its name | Renamed the fixture |
| mkversion failed in the monorepo | It expects its own git repo | `build-android.ps1` passes `TS_VERSION_LONG` / `TS_VERSION_GIT_HASH` |
| `tappervpn update` would install official Tailscale | Upstream's `clientupdate` | Disabled; our updater is `tappervpn-updater` |

## Still to do

- vp.tapper.co: DNS A record to the home public IP, port forward TCP 443
  (and UDP 41641) to tapper, HTTPS on headscale, then `server_url`.
- headscale shouldn't die when it can't download the relay map: keep a
  local copy under `derp.paths`.
- Rename the MagicDNS domain (`meshwall.internal`, the old project name).
- Test the router package on a real GL.iNet router.
