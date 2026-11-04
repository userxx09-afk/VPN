# Per-app routing: design (Windows first)

Status: **v1 implemented** 2026-10-18 (local-file policy; control-server delivery needs
the Headscale change below). Unit-tested; not yet tested end-to-end, which needs a
second node acting as gateway. See [What was built](#what-was-built-v1) at the end
for where the code differs from this design.

## Goal

Pick, per application, whether its internet traffic:

| Action    | Meaning |
|-----------|---------|
| `gateway` | Goes through the mesh to the active gateway (exit node). |
| `direct`  | Goes out the local network as if the mesh weren't there. |
| `block`   | Is dropped (the firewall part). |

Traffic to other mesh nodes (100.64.0.0/10, fd7a:115c:a1e0::/48) and to
100.100.100.100 is **never** affected. Per-app rules only decide what happens to
traffic that would otherwise use the gateway.

## Approach in one paragraph

When a gateway is active, tappervpnd already routes *all* of the PC's traffic into
its tunnel adapter, so every outgoing packet passes through tappervpnd in userspace.
On the first packet of each new flow we ask Windows which process owns that socket
(user-mode `GetExtendedTcpTable` / `GetExtendedUdpTable`, no driver), match it against
the rules, and cache the decision for the flow. `gateway` flows continue to WireGuard
unchanged. `direct` flows are absorbed into tappervpnd's userspace TCP/IP stack
(netstack), which re-dials them from a socket pinned to the physical NIC. `block`
flows are dropped. This works with **no kernel driver**, which is the main reason to
choose it for v1.

```
app ──► Windows TCP/IP ──► TapperVPN adapter (wintun) ──► tappervpnd
                                                          │
                     tstun: PreFilterPacketOutboundToWireGuardNetstackIntercept
                                                          │
                                         netstack.handleLocalPackets
                                                          │  (new) approuting.Classify(pkt)
                       ┌──────────────────────────────────┼──────────────────────┐
                    gateway                             direct                  block
                       │                                  │                       │
             filter → WireGuard → gateway        netstack terminates TCP/UDP     drop
                                                          │
                                         GetTCPHandlerForFlow / GetUDPHandlerForFlow
                                                          │
                                       netns dialer (IP_UNICAST_IF = physical NIC)
                                                          │
                                                     internet
```

## Policy format

One JSON document per node. It is cross-platform: each rule can name the app the way
each OS identifies it, and a client only looks at its own OS's block.

```jsonc
{
  "version": 1,
  "default": "direct",          // action for apps that match no rule
  "unidentified": "gateway",    // action when the owning process can't be found
  "rules": [                    // first match wins
    {
      "name": "Browsers via gateway",
      "action": "gateway",
      "apps": [
        { "windows": { "exe": "chrome.exe" },
          "macos":   { "bundleId": "com.google.Chrome" },
          "android": { "package": "com.android.chrome" } },
        { "windows": { "path": "%ProgramFiles%\\Mozilla Firefox\\firefox.exe" } }
      ]
    },
    {
      "name": "No telemetry",
      "action": "block",
      "apps": [ { "windows": { "exe": "CompatTelRunner.exe" } } ]
    }
  ]
}
```

- **"Only these apps use the gateway"**: `"default": "direct"` plus `gateway` rules.
- **"Everything except these apps"**: `"default": "gateway"` plus `direct` rules.
- `unidentified` decides fail-closed (`gateway` or `block`) vs fail-open (`direct`).

### Windows matchers (v1)

| Field  | Matches | Notes |
|--------|---------|-------|
| `exe`  | Image file name, case-insensitive (`chrome.exe`) | Simple, but any program can use that name. |
| `path` | Full image path, case-insensitive; expands `%ProgramFiles%`, `%LocalAppData%` etc.; `*` glob allowed | Preferred. |
| `signer` (v2) | Authenticode signer subject, e.g. `Google LLC` | Hardest to spoof. |
| `package` (v2) | UWP/MSIX package family name | For Store apps. |

Several fields in one matcher must **all** match.

### Where the policy comes from

1. **Control server (normal case).** Delivered as a node capability, so no protocol
   change is needed:
   - Capability name: `meshwall.dev/cap/app-routing`
   - Value: the JSON document above (`tailcfg.NodeCapMap` values are `[]json.RawMessage`).
   - Client reads it from `netmap.SelfNode.CapMap()` on every netmap update.
2. **Local file (development only).** `TS_DEBUG_APPROUTING_FILE=C:\path\rules.json`
   envknob. It is used only when the control server sends no policy, so an admin's
   policy can't be overridden locally.

If several values arrive (several matching `nodeAttrs` grants), their `rules` are
concatenated in policy order, and the first document's `default` / `unidentified` win.

### Headscale change needed

Headscale's `nodeAttrs` currently accepts bare capability names (`attr`) only,
not JSON values (`app`), which TapperVPN's own policy format supports. Add it:

- `headscale/hscontrol/policy/v2/types.go`, `NodeAttrGrant`: add
  `App tailcfg.NodeCapMap \`json:"app,omitempty"\``.
- `headscale/hscontrol/policy/v2/policy.go`, `NodeCapMap()`: merge `App` values into
  the node's cap map alongside `Attrs`.
- Validation: accept only the `meshwall.dev/cap/*` prefix for now.

Policy file then looks like:

```jsonc
"nodeAttrs": [
  { "target": ["tag:laptops"],
    "app": { "meshwall.dev/cap/app-routing": [ { "version": 1, "default": "direct", "rules": [ ... ] } ] } }
]
```

## Where it plugs into tappervpnd

New package: **`tappervpn/feature/approuting`**, registered like the other `feature/*`
packages (via `feature/condregister`), so it can be compiled out.

| File | Purpose |
|------|---------|
| `policy.go` (+ `_test.go`) | Types, JSON parsing, validation, rule matching. Pure Go, OS-independent, unit-tested. |
| `owner_windows.go` | Flow → process: local port → PID via `GetExtendedTcpTable` / `GetExtendedUdpTable`; PID → image path via `QueryFullProcessImageNameW`. Builds on `net/netstat/netstat_windows.go`, which has the TCP table today; UDP is a TODO there. |
| `flows.go` | Flow table: 5-tuple → decision, with idle timeouts (TCP removed after FIN/RST + grace period, UDP after 60s idle). |
| `approuting.go` | The classifier and the netstack handlers. |

Changes to existing upstream code (kept small so rebasing on upstream stays easy):

| Upstream file | Change |
|---|---|
| `wgengine/netstack/netstack.go`, `handleLocalPackets` | In the `default:` branch, call an optional `ns.ClassifyLocalPacket` hook. `direct` falls through to the existing "inject into netstack" path, `block` returns `filter.DropSilently`, and `gateway` returns `filter.Accept` (today's behavior). |
| `wgengine/netstack/netstack.go` | Set `GetTCPHandlerForFlow` / `GetUDPHandlerForFlow` (only `tsnet` uses them today; tappervpnd leaves them nil). For flows marked `direct`, the handler dials the destination with `netns.NewDialer` and splices bytes both ways. **It must not use the default `net.Dialer`.** That would follow the exit-node default route straight back into the tunnel. `netns` sets `IP_UNICAST_IF` to the non-TapperVPN default interface. |
| `ipn/ipnlocal/local.go` | On netmap change: extract the capability, parse it, and hand it to the feature. Classification only runs while an exit node is selected. |
| `net/netstat/netstat_windows.go` | Add the UDP table (`GetExtendedUdpTable`). |

### Classification, per packet

1. Feature off, or no exit node selected → `Accept` (unchanged).
2. Destination is a mesh address, quad-100, or a LAN address while "allow LAN access"
   is on → `Accept`.
3. Known flow → cached decision.
4. New flow (TCP SYN without ACK; unseen UDP 5-tuple) → look up the owner → match
   the rules → store the decision → apply it.
5. Other packets with no flow entry (e.g. a mid-stream TCP packet after a restart)
   → `default` action.

Owner lookup: keep a snapshot of the TCP/UDP tables keyed by local port, refreshed on
a miss and at most every ~50ms. Cache PID → path by (PID, process creation time) so a
reused PID doesn't inherit someone else's rules.

## Known limitations and risks

- **DNS doesn't go with the app.** Windows resolves names in the DNS Client service
  (`svchost.exe`), not in the app, so DNS lookups follow `default`. In
  "only these apps use the gateway" mode, the selected apps' DNS lookups go direct.
  Fix (v2): make MagicDNS (100.100.100.100) forward upstream queries through the
  gateway.
- **Direct traffic pays a userspace cost.** In "only these apps use the gateway" mode,
  most traffic is `direct` and goes through netstack's TCP proxy, which is slower
  and uses more CPU than the kernel. Fine for v1; the v3 driver removes it.
- **Very short UDP flows** may finish before the table snapshot shows them; those use
  `unidentified`.
- **Shared processes:** Windows services hosted in `svchost.exe` all look the same;
  matching them needs service-name lookup (v2).
- **One gateway at a time.** TapperVPN routes 0.0.0.0/0 to a single exit node, so v1
  supports one active gateway. Choosing a gateway per app (app A → node X,
  app B → node Y) would need netstack to proxy those flows to the chosen gateway over
  the mesh (v2+).
- **Anyone with admin rights on the PC can bypass this.** It's a policy tool, not
  protection against the machine's own administrator.

## Roadmap

| Phase | Scope |
|-------|-------|
| **v1** | Policy parsing + tests; Windows owner lookup (`exe`/`path`); netstack classifier with `gateway`/`direct`/`block`; local-file policy; `tappervpn debug approuting` command to show flows and decisions. |
| v1.1 | Headscale `nodeAttrs.app` support; policy pushed from the control server. |
| v2 | `signer` / `package` matchers; svchost service names; DNS through gateway; per-app gateway choice. |
| v3 | Signed WFP callout driver (`ALE_CONNECT_REDIRECT` / `ALE_BIND_REDIRECT`) so `direct` flows bypass the tunnel in the kernel with no userspace cost. Needs an EV code-signing certificate and Microsoft attestation signing. |
| Other OSes | Same policy document. Android: `VpnService.Builder.addAllowedApplication()` / `addDisallowedApplication()`. macOS: `NETransparentProxyProvider` using `sourceAppSigningIdentifier`. iOS: managed per-app VPN (MDM) only. |

## Implementation order for v1

1. `policy.go` + table-driven tests (no Windows dependencies, quick to verify).
2. UDP table in `net/netstat`; `owner_windows.go` with a small CLI test that prints
   the owner of live connections.
3. `ClassifyLocalPacket` hook + flow table, with `block` only (simplest action to verify).
4. `direct` via `GetTCPHandlerForFlow` / `GetUDPHandlerForFlow` + `netns` dialer.
5. Wire up `ipnlocal` and the envknob file; end-to-end test with two nodes, one as
   gateway, checking which public IP each app sees.

## What was built (v1)

Code: `tappervpn/feature/approuting/` (registered on Windows via
`feature/condregister/maybe_approuting.go`; can be omitted with `ts_omit_approuting` or
disabled at runtime with `TS_DISABLE_FEATURE=approuting`).

| File | Contents |
|------|----------|
| `policy.go` | Policy types, strict parsing (unknown fields rejected), validation, `Merge`, compile to a per-OS `Matcher`. |
| `owner.go`, `owner_windows.go` | Socket-table snapshots (`GetExtendedTcpTable`/`GetExtendedUdpTable`, PID-only tables), wildcard and dual-stack fallback, rate-limited refresh (10ms), PID→path cache (2s). |
| `flows.go` | Flow table with TCP (10 min idle, 10s after FIN/RST) and UDP (2 min idle) timeouts. |
| `approuting.go` | `Router`: `Classify` hook, `direct` TCP relay (upstream dialed *before* the app's handshake completes, so failures look like a refused connection) and UDP relay (replies accepted only from the flow's destination). |
| `extension.go` | ipnext extension: installs the netstack hooks, reads the policy from the self node's `meshwall.dev/cap/app-routing` capability or `TS_DEBUG_APPROUTING_FILE`, tracks whether an exit node is selected. Turns app routing **off** if the policy is invalid. |

Differences from the design above:

- **Owner lookup lives in the feature package**, not in `net/netstat`. That's fewer upstream
  edits to carry, and the cheaper PID-only tables are enough.
- **Two extra netstack changes were needed** (`wgengine/netstack/netstack.go`):
  1. `shouldSendToHost` sends replies on intercepted flows (to one of this node's IPs, from a
     non-TapperVPN address) back to Windows. Without this, replies hit the self-loopback path
     and never reach the app.
  2. The `GetUDPHandlerForFlow` intercept path now releases the destination address that
     netstack registered for the flow (`forwardUDP` already did); otherwise every distinct
     destination leaked an address.
- netstack's own `forwardUDP` can't be reused: it uses an unbound socket (it would loop back
  into the tunnel) and tries to bind the app's own source port.
- **ICMP and other non-TCP/UDP traffic always goes to the gateway**; netstack can't proxy
  arbitrary ICMP.
- **A TCP packet with no flow entry that isn't a SYN gets `default`** (for connections opened
  before app routing turned on). If that's `direct`, netstack resets it and the app reconnects.
- **Policy changes apply to new flows only**; existing flows keep their decision until they end.
- **IPv6 caveat:** with no IPv6 default route on the physical NIC, `netns` can't bind a `direct`
  IPv6 socket, and that traffic follows the exit node's `::/0` route instead.

Tests: `go test ./feature/approuting/` (policy, owner lookup with a fake socket table plus a
live Windows socket test, flow table, classifier, TCP and UDP relays). `go vet` passes for
`wgengine/netstack`. Its tests compile, but this PC's Windows Application Control policy
blocks the netstack test binary from running.

### Trying it

1. Stop the node (Ctrl+C in its window), then start it with a policy:
   `.\scripts\start-node.ps1 -Policy examples\approuting-chrome-only.json -LogFlows`
2. On a second node, advertise it as a gateway: `tappervpn up --advertise-exit-node`, then approve
   it in Headscale (`nodes list-routes`, `nodes approve-routes`).
3. On this PC: `bin\tappervpn.exe set --exit-node=<gateway>`.
4. Check a what's-my-IP site in Chrome (should show the gateway's IP) and in Edge (should show
   your own). The node window logs each connection's decision.

## Choosing a gateway per app (added 2026-10-30)

A `gateway` rule can name the node to use instead of the selected exit node:

```jsonc
{ "name": "Chrome via the Fire TV", "action": "gateway", "gateway": "shanes-fire-tv-cube",
  "apps": [ { "windows": { "exe": "chrome.exe" } } ] }
```

A rule without `"gateway"` keeps using the selected exit node. `"gatewayUnavailable"`
(policy level, default `block`) decides what happens when the named gateway is offline or
unknown, or the destination is IPv6. It never silently falls back to a different path unless
you set it to `gateway` (use the exit node) or `direct`. Example:
[examples/approuting-per-gateway.json](../examples/approuting-per-gateway.json).

### How it works

TapperVPN sends `0.0.0.0/0` to only one exit node, so additional gateways use **4via6**:

1. Each named gateway advertises "all of IPv4 behind site *N*": the route
   `MapVia(N, 0.0.0.0/0)` = `fd7a:115c:a1e0:b1a:0:N::/96` (N is a site ID unique to that
   gateway).
2. The client intercepts flows for that gateway into netstack (like `direct`) and re-opens
   them from netstack to `[fd7a:115c:a1e0:b1a:0:N:<ipv4>]:<port>`. That never touches the
   Windows routing table, so a missing route can't leak traffic out the physical NIC.
3. The gateway's netstack sees a 4via6 destination it serves, unmaps it to the IPv4 address,
   and dials out from its own internet connection (TCP, UDP and ping are all handled
   upstream).

Gateway names are matched case-insensitively against each peer's MagicDNS name (without the
domain), computed name, and OS hostname. A name shared by two gateways is ignored rather
than guessed. Peers reported offline are skipped.

### Setting up a gateway

On the gateway node (site ID 1 here; use a different number per gateway):

```bash
tappervpn debug via 1 0.0.0.0/0
```

That prints the route (`fd7a:115c:a1e0:b1a:0:1::/96`). Advertise it, adding it to any routes
the node already advertises (and keeping `--advertise-exit-node` if it's also an exit node):

```bash
tappervpn set --advertise-routes=fd7a:115c:a1e0:b1a:0:1::/96
```

Approve it on the server: `headscale nodes list-routes`, then `headscale nodes approve-routes`.

On each **client**: `tappervpn set --accept-routes`, so WireGuard sends the via range to that
gateway. (The client-side route hooks are already used by another upstream feature, so the
policy can't inject them itself.)

### Limits

- **IPv4 only.** 4via6 carries IPv4 destinations; IPv6 flows on a named-gateway rule get
  `gatewayUnavailable`.
- **Still needs an exit node selected on the client.** That's what routes the PC's internet
  traffic into tappervpnd in the first place. Named gateways override it per app.
- **The Android/Fire TV app can't advertise routes yet.** Its UI only offers "run as exit
  node", so the Fire TV can be the *selected exit node* today but not a *named* gateway until
  the app gets a setting for it.
- Gateway resolution follows the netmap. If a gateway goes offline, new flows get
  `gatewayUnavailable`; flows already in progress keep their decision until they end.
