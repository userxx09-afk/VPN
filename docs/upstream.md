# Bringing in new upstream versions

`tappervpn/`, `headscale/` and `tappervpn-android/` started as these upstream
commits, renamed by `scripts/rebrand.py`:

| Folder | Upstream | Base commit |
|---|---|---|
| `tappervpn/` | https://github.com/tailscale/tailscale | `e3c3ef1` (2026-10-18) |
| `headscale/` | https://github.com/juanfont/headscale | `691ef56` (2026-10-18) |
| `tappervpn-android/` | https://github.com/tailscale/tailscale-android | `264102c` |

Our changes since then are in this repo's git history. Before the move to
this repo, the forks had their own histories; those are kept (outside the
repo) in `meshwall-backup-2026-11-04/`.

## Updating to a newer upstream

1. Check out upstream at the old base commit and at the new version, in two
   scratch folders, and run `python scripts/rebrand.py <folder>` on both.
2. Diff the two renamed trees and apply that diff to our folder
   (`git diff --no-index old new > up.patch`, then `git apply -3 up.patch`
   inside our folder, adjusting paths). Resolve conflicts with our changes.
3. Update the base commit in the table above.
4. Build and test (`go build ./...`, `go test ./...`, Android build), then
   bump `VERSION` and release.

## What the rename keeps

See the top of `scripts/rebrand.py`. In short: copyright notices, other
modules Tailscale publishes (`github.com/tailscale/wireguard-go`, ...), and
real host names: TapperVPN still uses Tailscale's public DERP relays
(`server/config.yaml`, `derp.urls`), and the relays' captive-portal check
headers (`X-Tailscale-Challenge`/`-Response`) must stay as they are.

## Removed or disabled

- `tappervpn/clientupdate`: installs Tailscale's official packages, which
  would replace TapperVPN. `NewUpdater` always refuses; `tappervpn/updater` is
  our updater.
- `headscale/hscontrol/tailsql.go` (debug SQL UI): it needs upstream
  `tailscale.com` types.
- `cmd/derper`, `cmd/derpprobe`: the `--secrets-url` (setec) option, for the
  same reason. `--mesh-psk-file` still works.
