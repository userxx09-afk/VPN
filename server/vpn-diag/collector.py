#!/usr/bin/env python3
"""TapperVPN diagnostics collector, run as root on the server (vpn-diag.service).

Every few seconds it records, for every device on the VPN:
  - online or not, and the path to it: direct (ip:port) or through a relay (DERP)
  - round-trip time (tappervpn ping), how much it's sending and receiving right now
  - its open connections through this server: to the internet when it uses the
    server as exit node, or to the server's own apps (from the kernel's
    connection tracker); "no reply" ones are blocked or unreachable
and keeps a log of notable events (went offline, fell back to a relay, slow).

The admin page (vpn-admin, which has no privileges) reads the results:
  /run/vpn-diag/state.json       latest snapshot
  /var/lib/vpn-diag/events.jsonl event log, one JSON object per line
Stdlib only.
"""
import ipaddress
import json
import os
import re
import socket
import subprocess
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone

STATE = "/run/vpn-diag/state.json"
EVENTS = "/var/lib/vpn-diag/events.jsonl"
SAMPLE_SECS = 5
PING_SECS = 30
SLOW_MS = 250          # ping above this is logged as slow
MAX_EVENTS = 5000
MAX_FLOWS = 400
VPN_NETS = [ipaddress.ip_network("100.64.0.0/10"), ipaddress.ip_network("fd7a:115c:a1e0::/48")]
PING_RE = re.compile(r"pong from \S+ \((\S+)\) via (.+?) in ([0-9.]+)ms")


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def run(cmd, timeout=10):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout).stdout
    except Exception:
        return ""


def event(kind, device, msg, **extra):
    rec = {"at": now(), "kind": kind, "device": device, "msg": msg, **extra}
    with open(EVENTS, "a") as f:
        f.write(json.dumps(rec) + "\n")


def trim_events():
    try:
        with open(EVENTS) as f:
            lines = f.readlines()
        if len(lines) > MAX_EVENTS:
            with open(EVENTS + ".tmp", "w") as f:
                f.writelines(lines[-MAX_EVENTS:])
            os.replace(EVENTS + ".tmp", EVENTS)
    except FileNotFoundError:
        pass


def in_vpn(ip):
    try:
        a = ipaddress.ip_address(ip)
    except ValueError:
        return False
    return any(a in n for n in VPN_NETS)


# --- reverse DNS for connection destinations, cached, never blocking a sample ---
_rdns = {}
_rdns_pool = ThreadPoolExecutor(4)


def rdns(ip):
    v = _rdns.get(ip)
    if v is None:
        _rdns[ip] = ""  # pending
        def look():
            try:
                _rdns[ip] = socket.gethostbyaddr(ip)[0]
            except Exception:
                _rdns[ip] = "-"
        _rdns_pool.submit(look)
        return ""
    return "" if v == "-" else v


def peers():
    """Devices as this server sees them, keyed by VPN IPv4."""
    out = run(["tappervpn", "status", "--json"])
    try:
        d = json.loads(out)
    except ValueError:
        return None, {}
    res = {}
    self_ips = (d.get("Self") or {}).get("TapperVPNIPs") or []
    for p in (d.get("Peer") or {}).values():
        ips = p.get("TapperVPNIPs") or []
        v4 = next((i for i in ips if "." in i), ips[0] if ips else "")
        if not v4:
            continue
        direct = p.get("CurAddr") or ""
        res[v4] = {
            "ip": v4,
            "ips": ips,
            "name": (p.get("DNSName") or "").split(".")[0] or p.get("HostName"),
            "host": p.get("HostName"),
            "os": p.get("OS"),
            "online": bool(p.get("Online")),
            "active": bool(p.get("Active")),
            "path": ("direct " + direct) if direct else ("relay " + (p.get("Relay") or "?")) if p.get("Online") else "",
            "direct": bool(direct),
            "rx": int(p.get("RxBytes") or 0),   # bytes from the device to this server
            "tx": int(p.get("TxBytes") or 0),   # bytes from this server to the device
            "handshake": p.get("LastHandshake"),
            "lastSeen": p.get("LastSeen"),
        }
    return self_ips, res


def ping(ip):
    out = run(["tappervpn", "ping", "-c", "1", "--timeout", "3s", ip], timeout=6)
    m = PING_RE.search(out)
    if not m:
        return None
    via = m.group(2)
    return {"ms": float(m.group(3)), "via": via, "relay": via.startswith("DERP")}


def flows(self_ips):
    """Open connections from VPN devices through this server (conntrack)."""
    out = run(["conntrack", "-L", "-o", "extended"], timeout=10)
    res = []
    for line in out.splitlines():
        parts = line.split()
        if len(parts) < 4:
            continue
        proto = parts[2]
        state = parts[5] if len(parts) > 5 and parts[5].isupper() and "=" not in parts[5] else ""
        kv = [p.split("=", 1) for p in parts if "=" in p]
        orig, reply = {}, {}
        for k, v in kv:
            (orig if k not in orig else reply).setdefault(k, v)
        src = orig.get("src", "")
        if not in_vpn(src):
            continue
        dst = orig.get("dst", "")
        if dst in self_ips:
            where = "server"
        elif in_vpn(dst):
            where = "vpn"
        else:
            where = "internet"
        res.append({
            "src": src, "dst": dst, "dport": orig.get("dport", ""), "proto": proto,
            "state": state, "where": where, "host": rdns(dst) if where == "internet" else "",
            "up": int(orig.get("bytes", 0)), "down": int(reply.get("bytes", 0)),
            "noReply": "[UNREPLIED]" in line,
        })
    res.sort(key=lambda f: (f["noReply"], f["up"] + f["down"]), reverse=True)
    return res


def main():
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    os.makedirs(os.path.dirname(EVENTS), exist_ok=True)
    trim_events()
    event("start", "", "Diagnostics collector started")
    have_conntrack = bool(run(["sh", "-c", "command -v conntrack"]).strip())
    prev, pings, last_ping, last_trim = {}, {}, 0.0, time.time()
    noreply_seen = {}
    lock = threading.Lock()

    def ping_all(ips):
        with ThreadPoolExecutor(8) as ex:
            for ip, r in zip(ips, ex.map(ping, ips)):
                with lock:
                    old = pings.get(ip)
                    pings[ip] = r
                if r and r["ms"] >= SLOW_MS and not (old and old["ms"] >= SLOW_MS):
                    event("slow", ip, f"Slow: {r['ms']:.0f} ms round trip via {r['via']}", ms=r["ms"])
                if r is None and old is not None:
                    event("noping", ip, "Stopped answering pings")

    while True:
        t0 = time.time()
        self_ips, cur = peers()
        if self_ips is None:
            time.sleep(SAMPLE_SECS)
            continue
        for ip, p in cur.items():
            o = prev.get(ip)
            if o:
                dt = max(t0 - o["_t"], 0.001)
                p["rxRate"] = max(p["rx"] - o["rx"], 0) / dt
                p["txRate"] = max(p["tx"] - o["tx"], 0) / dt
                if p["online"] != o["online"]:
                    event("online" if p["online"] else "offline", ip,
                          f"{p['name']} {'came online' if p['online'] else 'went offline'}")
                elif p["online"] and p["direct"] != o["direct"] and p["path"] and o["path"]:
                    event("path", ip, f"{p['name']}: {o['path']} -> {p['path']}"
                          + ("" if p["direct"] else " (relayed: slower)"))
            else:
                p["rxRate"] = p["txRate"] = 0.0
            p["_t"] = t0

        if t0 - last_ping >= PING_SECS:
            last_ping = t0
            threading.Thread(target=ping_all, args=([ip for ip, p in cur.items() if p["online"]],), daemon=True).start()

        fl = flows(self_ips) if have_conntrack else []
        # Count IPv6 connections under the device's IPv4 address; drop the server's own.
        alias = {i: ip for ip, p in cur.items() for i in p["ips"]}
        fl = [dict(f, src=alias.get(f["src"], f["src"]), via6=":" in f["src"]) for f in fl if f["src"] not in self_ips]
        # Log a device's connections that get no reply (once per destination per 10 min).
        for f in fl:
            if f["noReply"] and f["where"] != "vpn":
                k = (f["src"], f["dst"], f["dport"])
                if t0 - noreply_seen.get(k, 0) > 600:
                    noreply_seen[k] = t0
                    name = cur.get(f["src"], {}).get("name", f["src"])
                    event("noreply", f["src"], f"{name}: no reply from {f['host'] or f['dst']}:{f['dport']}/{f['proto']} (blocked or unreachable)")

        with lock:
            pv = dict(pings)
        devices = []
        for ip, p in cur.items():
            d = {k: v for k, v in p.items() if not k.startswith("_")}
            d["ping"] = pv.get(ip)
            d["flows"] = sum(1 for f in fl if f["src"] == ip)
            devices.append(d)
        snap = {"at": now(), "conntrack": have_conntrack, "devices": devices, "flows": fl[:MAX_FLOWS]}
        tmp = STATE + ".tmp"
        with open(tmp, "w") as f:
            json.dump(snap, f)
        os.chmod(tmp, 0o644)
        os.replace(tmp, STATE)

        prev = cur
        if t0 - last_trim > 3600:
            last_trim = t0
            trim_events()
            noreply_seen = {k: v for k, v in noreply_seen.items() if t0 - v < 600}
        time.sleep(max(SAMPLE_SECS - (time.time() - t0), 0.5))


if __name__ == "__main__":
    main()
