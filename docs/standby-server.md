# Standby VPN server on fm-workstation (plan)

Goal: if tapper (Chicago) is down, the VPN keeps working: devices still find
each other, new sign-ins and dashboard changes still work, and there's still
an exit node. Decided Nov 15 2026: **full standby**, running in an **Ubuntu VM
in Hyper-V on fm-workstation** (Fort Madison).

## What breaks today when tapper is down

| Piece | Today | With the standby |
|---|---|---|
| Devices already connected | keep working (`cache-network-maps` in policy.hujson) | same |
| Relay for devices that can't connect directly | down (relay is on tapper) | second relay region on the standby |
| Sign-ins, approvals, tags, dashboard | down | standby control server takes over |
| Exit node through Chicago | down | exit node through Fort Madison |

## How it fits together

1. **VM**: `tapper-standby` on fm-workstation. Ubuntu Server 24.04, 2 vCPU,
   4 GB RAM, 40 GB disk, external switch so it gets its own LAN address.
   fm-workstation has 16 GB RAM, 14 threads and ~140 GB free, so this fits.
   Hyper-V needs turning on once (admin + one reboot of fm-workstation).
2. **Same identity**: the standby runs the same headscale build with copies of
   tapper's `noise_private.key` and `derp_server_private.key`, so to the
   devices it *is* the same server. The keys come from the private repo
   (`userxx09-afk/private`, age-encrypted), never from this repo.
3. **State copy**: a timer on tapper sends a consistent copy of
   `/var/lib/headscale/db.sqlite` (`sqlite3 .backup`) plus the live
   `policy.hujson` to the standby over the VPN every 5 minutes. The standby
   keeps headscale **stopped** while tapper is healthy (two servers writing the
   same network would split it).
4. **Taking over**: a watchdog on the standby checks tapper's control port and
   relay over the VPN and over the internet. After 5 minutes of tapper not
   answering *both*, it starts headscale with the newest copy. When tapper is
   back, the standby stops and its changes are copied back to tapper before
   tapper's headscale starts serving again (a "hand back" step, run from the
   dashboard).
5. **How devices find it**: today every device knows one address,
   `https://vp.tapper.co` (Chicago's public IP). The apps get a second one,
   `https://vp2.tapper.co` (Fort Madison's public IP, port 443 forwarded to the
   VM). The client's control-server finder (local network first, then
   vp.tapper.co) learns to try vp2.tapper.co when vp.tapper.co doesn't answer.
   Same keys, so nobody has to sign in again. This needs a VPN app release.
6. **Relay**: the standby also runs a relay, added to the relay map as a
   second region, so devices use whichever is up even before failover.
   Needs UDP 3478 forwarded at Fort Madison too.
7. **Exit node**: the VM offers itself as an exit node; routers that send all
   traffic through tapper fall back to it (router `update`/watchdog change).
8. **Dashboard**: shows the standby's state (copy age, which server is in
   charge) and has the "hand back to tapper" button.

## Needed from you

- Admin approval on fm-workstation to turn on Hyper-V (one reboot).
- On the Fort Madison router: forward TCP 443 and UDP 3478 to the VM.
- A DNS record `vp2.tapper.co` → Fort Madison's public IP (GoDaddy).
- The Fort Madison public IP is static, or we add a dynamic-DNS updater.

## Build order

1. VM + headscale + relay on the standby; the state copy timer on tapper.
2. Second relay region (useful on its own, no app change).
3. Watchdog + takeover + hand back, tested by stopping headscale on tapper.
4. App release: control-server fallback to vp2.tapper.co.
5. Exit-node fallback for routers; dashboard view.
