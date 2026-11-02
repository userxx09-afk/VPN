# meshwall

An application-aware firewall that builds a WireGuard mesh between devices.
Any capable node can be the **gateway** (exit node), and you choose **which
applications** send their traffic through it.

Built on forks of:

| Folder        | Upstream                              | Role                                   |
|---------------|---------------------------------------|----------------------------------------|
| `tailscale/`  | github.com/tailscale/tailscale (BSD-3) | Node daemon + CLI (`tailscaled`, `tailscale`) |
| `headscale/`  | github.com/juanfont/headscale (BSD-3)  | Self-hosted control server             |

Both were shallow-cloned on 2026-10-18 (tailscale `e3c3ef1`, headscale `691ef56`).

## Prerequisites (Windows dev box)

- **Go 1.27.2+** (both `go.mod` files require it): https://go.dev/dl/
- Git (installed)
- Headscale officially ships only Linux/macOS server builds, but it builds and runs natively
  on Windows, which is fine for development. Use Linux (WSL, a VPS, a Pi) for production.

## Building

```bash
cd tailscale && go build -o ../bin/tailscaled.exe ./cmd/tailscaled && go build -o ../bin/tailscale.exe ./cmd/tailscale
cd ../headscale && go build -o ../bin/headscale.exe ./cmd/headscale
```

`tailscaled.exe` also needs **`bin/wintun.dll`** (the tunnel adapter driver, 0.14.1, amd64) from
https://www.wintun.net/builds/wintun-0.14.1.zip (zip SHA-256 `07c25618…24ef51`, DLL signed by
WireGuard LLC). Without it, the daemon only runs with `--tun=userspace-networking`.

## Running the dev mesh (Windows)

| Step | Command | Admin? |
|---|---|---|
| 1. Control server | `scripts\start-server.ps1` | No |
| 2. Node daemon (leave running) | `scripts\start-node.ps1` (optional: `-Policy examples\approuting-chrome-only.json -LogFlows` for per-app routing) | Yes |
| 3. Join the mesh | `scripts\join.ps1` (add `-Gateway` to advertise as gateway) | Yes |

- Server config: `server/config.yaml` (listens on `0.0.0.0:8080`, advertises `http://192.168.5.207:8080`).
  Data paths in it must be **relative** (to the config's folder): Headscale treats `C:/...` as relative.
- Join key: `server/data/preauthkey.json` (reusable, expires 2026-10-21). Make a new one with
  `bin\headscale.exe -c server\config.yaml preauthkeys create --user 1 --reusable --expiration 72h`.
- After a node advertises itself as a gateway, approve it on the server:
  `bin\headscale.exe -c server\config.yaml nodes list-routes`, then `nodes approve-routes`.
- Use a gateway from another node: `bin\tailscale.exe set --exit-node=<gateway-name>`.
- Don't commit `server/data/` or `node/state/`; they hold private keys.

Note: the official Tailscale Windows tray GUI is not open source. We use the open-source
`tailscaled` daemon and `tailscale` CLI, and will build our own UI.

## Where our changes go

| Feature | Key upstream code |
|---|---|
| Gateway (exit node) selection | `tailscale/ipn/ipnlocal/local.go`, `node_backend.go` (`ExitNodeID` prefs) |
| Windows routing | `tailscale/wgengine/router/osrouter/router_windows.go` |
| Windows firewall (WFP) | `tailscale/wf/firewall.go`, used from `cmd/tailscaled/tailscaled_windows.go` |
| Userspace packet path | `tailscale/wgengine/netstack`, `tailscale/net/tstun` |
| Domain-based routing (existing) | `tailscale/appc/` (App Connectors) |
| Per-app policy distribution | `headscale/hscontrol/` (policy + node map) |

## Roadmap

1. **Baseline mesh**: Headscale in WSL; 2+ Windows nodes joined; one advertised as gateway.
2. **Per-app policy model**: add an app list (e.g. `chrome.exe`, `C:\Program Files\...\app.exe`)
   per node in Headscale policy, and push it to clients in the netmap.
3. **Windows per-app routing**: design in [docs/per-app-routing.md](docs/per-app-routing.md)
   (v1 is user-mode only: socket owner lookup + netstack; WFP driver comes later).
   Earlier notes on WFP:
   - Step A (user-mode WFP, like `wf/`): allow/block per app on the tunnel interface.
   - Step B: send only the chosen apps to the gateway. This needs either a signed WFP callout
     driver for redirection, or binding each app's sockets to the tunnel through a local proxy.
4. **Own UI**: Windows tray app for choosing the gateway and app list.
5. **Android**: `VpnService.addAllowedApplication()` per-app (fork of tailscale-android).
6. **macOS**: `NETransparentProxyProvider` system extension.
7. **iOS**: full-tunnel or domain-based only. Per-app VPN needs MDM; iOS cannot be a gateway.

## Known quirks

- `headscale/`: one test fixture whose name contains `>` can't be checked out on Windows
  (`hscontrol/testdata/apiv1_golden/...->_400_parity.json`). It shows up as a staged
  deletion in `git status`. Harmless for building; don't commit that deletion.
- Before releasing, rename and rebrand: the "Tailscale" name and logo are trademarks.

## Production server (Ubuntu)

Since 2026-11-01 the control server runs on the Ubuntu 24.04 VM `frank` (192.168.5.207, ssh alias `tapper`),
not on the Windows dev box.

| What | Where on the server |
|---|---|
| Binaries (built here with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`) | `/usr/local/bin/headscale`, `tailscale`, `tailscaled` |
| Config / policy | `/etc/headscale/config.yaml`, `/etc/headscale/policy.hujson` |
| Database + noise key | `/var/lib/headscale/` |
| Service | `systemctl status headscale` (runs as user `headscale`, starts on boot) |
| Admin CLI | `headscale -c /etc/headscale/config.yaml nodes list`, run as the `headscale` user |

Clients use `http://192.168.5.207:8080` (LAN only until HTTPS and port forwarding are set up).
