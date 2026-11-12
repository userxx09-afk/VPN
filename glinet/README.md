# TapperVPN for GL.iNet routers

Puts devices that can't run the app (smart TVs, consoles, streaming boxes) on
TapperVPN. The router joins the VPN and sends chosen LAN traffic through an
exit node:

| Mode      | What uses the exit node                                          |
|-----------|------------------------------------------------------------------|
| `all`     | every device on the router's LAN                                 |
| `clients` | chosen devices (by MAC), or whole networks such as the guest Wi-Fi |
| `domains` | connections to chosen sites (e.g. `espn.com`) from any device    |
| `none`    | nothing; the router just joins (e.g. to offer itself as an exit node) |

A dedicated Wi-Fi works well with `clients`: turn on the guest network, add
zone `guest`, and any device that joins it is tunneled.

## Build

```
python glinet/build.py              # builds tappervpnd for each CPU, then the packages
python glinet/build.py aarch64      # one target only
python glinet/build.py --skip-go    # repackage without rebuilding the binaries
```

Output in `bin/glinet/`: `tappervpn_<version>_<cpu>.ipk`.

| Package    | Routers |
|------------|---------|
| `aarch64`  | Flint 2 (MT6000), Beryl AX (MT3000), Slate AX (AXT1800), Flint (AX1800), Brume 2, most current models |
| `armv7`    | older ARM models |
| `mipsel`, `mips` | older MIPS models; need about 20 MB free flash |

Models with 16 MB of flash (Mango, Shadow, older Slate) are too small.
If unsure, run `uname -m` on the router (SSH) or try `aarch64` first: the
install refuses a package built for the wrong CPU.

## Install

1. Turn off the router's built-in Tailscale (GL.iNet admin > Applications > Tailscale).
   The two use the same routing table.
2. Open LuCI: GL.iNet admin > System > Advanced Settings > Go to LuCI.
3. System > Software > Upload Package, choose the `.ipk`, Install.
   Or over SSH: `scp` it to `/tmp` and run `opkg install /tmp/tappervpn_*.ipk`.
4. Services > TapperVPN: set the server address and a sign-in key
   (`headscale preauthkeys create --user <user>`), tick Enabled, Save & Apply.
5. Pick an exit node and mode, Save & Apply.

Settings live in `/etc/config/tappervpn` and are kept across upgrades. The
status box shows `tappervpn status`; logs: `logread -e tappervpn`.

## Updates

With `option auto_update '1'` (the default), cron runs
`/usr/libexec/tappervpn/update` once a day between 4 and 5 AM. It fetches the
latest GitHub release, checks `SHA256SUMS.sig` with `usign` against
`/usr/share/tappervpn/release.pub`, checks the package's hash, and installs
`tappervpn-glinet-<cpu>.ipk` with opkg. Settings are kept. Run
`/usr/libexec/tappervpn/update check` to see if an update is waiting; logs:
`logread -e tappervpn-update`. Set `auto_update` to `0` to turn it off.

## From the admin console

Each router checks in with the admin console once a minute
(`/usr/libexec/tappervpn/checkin`, cron), so its card on the console shows the
router model, firmware, TapperVPN version, mode and exit node, plus three buttons:
**Update now** (install the newest signed release), **Check for update** and
**Re-apply settings** (sign in again and rebuild the routing, like Save & Apply).
The router picks the request up on its next check-in and reports how it went.

The console can't log in to the router; the router asks it. It finds the console at
`admin_url` (default `http://vpnadmin.tapper.co:8096`), looked up through 1.1.1.1
because the router's own DNS may refuse private-address answers, and falls back to
`admin_ip` (default `100.64.0.2`, tapper's VPN address). The check-in travels over
the VPN; the console uses the sender's VPN address to know which router it is and
ignores check-ins from anywhere else.

One-time DNS setup in Cloudflare (zone tapper.co): an **A** record `vpnadmin` →
`100.64.0.2`, proxy status **DNS only** (grey cloud). It never goes through the
Cloudflare tunnel, which the console refuses anyway; the address only works over the VPN.

Turn it off with `option remote_control '0'` (Services > TapperVPN: "Allow the
admin console to update this router").

## How it works

- `tappervpnd` is our Tailscale fork built for Linux with only router features
  (`build.py` FEATURES), about 17 MB. It runs on interface `tapper0`, port
  41642, state in `/etc/tappervpn`, so it doesn't clash with GL.iNet's files.
- `/usr/libexec/tappervpn/apply` signs in (`tappervpn up`), then sets up:
  - a firewall zone `tappervpn` that masquerades LAN devices behind the
    router's VPN address (exit nodes only accept VPN addresses);
  - for `clients`/`domains`: firewall MARK rules plus `ip rule`s 5262–5265
    so only marked traffic reaches tappervpnd's table 52;
  - for `domains`: dnsmasq adds each looked-up address of those sites to a
    firewall set, and resolves them through the exit node (100.100.100.100)
    so the sites see the exit node's location.
- Works with fw3 (iptables, OpenWrt 21.02) and fw4 (nftables, 22.03+).
- IPv6 from tunneled devices is rejected so apps fall back to IPv4 at once.

## Limits

- One exit node per router at a time.
- `domains` needs `dnsmasq-full` (standard on GL.iNet firmware) and only
  catches devices that use the router for DNS. Apps with built-in DNS
  (Chrome secure DNS, some TV apps) skip it; GL.iNet's "Override DNS
  settings for all clients" option helps.
- Not tested on a real router yet.
