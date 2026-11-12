#!/usr/bin/env python3
"""TapperVPN admin page: see devices and manage them through headscale's API.

Only answers visitors on the home LAN or the VPN itself, never visits that come
through the Cloudflare tunnel. Stdlib only.

Env:
  PORT          listen port (default 8096)
  HS_URL        headscale base URL (default http://127.0.0.1:8080)
  HS_KEY_FILE   file holding the headscale API key
                (default $CREDENTIALS_DIRECTORY/api-key, from systemd LoadCredential)
"""
import ipaddress
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
PORT = int(os.environ.get("PORT", "8096"))
HS_URL = os.environ.get("HS_URL", "http://127.0.0.1:8080").rstrip("/")
KEY_FILE = os.environ.get(
    "HS_KEY_FILE", os.path.join(os.environ.get("CREDENTIALS_DIRECTORY", "/etc/vpn-admin"), "api-key")
)
DEFAULT_USER = "admin"

ALLOWED_NETS = [ipaddress.ip_network(n) for n in (
    "127.0.0.0/8", "::1/128",
    "10.0.0.0/24",            # home LAN
    "100.64.0.0/10",          # VPN IPv4
    "fd7a:115c:a1e0::/48",    # VPN IPv6
)]
VPN_NETS = ALLOWED_NETS[3:5]   # router check-ins only count over the VPN
EXIT_ROUTES = {"0.0.0.0/0", "::/0"}
NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,62}$")
AUTH_RE = re.compile(r"^hskey-authreq-[A-Za-z0-9_-]{6,64}$")
ID_RE = re.compile(r"^[0-9]{1,10}$")


def api_key():
    with open(KEY_FILE) as f:
        return f.read().strip()


def hs(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        HS_URL + path, data=data, method=method,
        headers={"Authorization": "Bearer " + api_key(), "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            raw = r.read()
            return 200, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            j = json.loads(raw)
            errs = j.get("errors") or []
            msg = (errs and errs[0].get("message")) or j.get("message") or j.get("detail") or raw.decode()
        except Exception:
            msg = raw.decode(errors="replace")
        if "registration cache" in msg:
            msg = "No device is waiting with that code. It may have expired; log in again on the device."
        return e.code, {"error": msg.strip()[:300]}
    except Exception as e:
        return 502, {"error": f"headscale not reachable: {e}"}


def load_releases():
    """releases.json (made by install.sh from git): release commits and the newest version."""
    try:
        with open(os.path.join(HERE, "releases.json")) as f:
            d = json.load(f)
        return {"commits": dict(d.get("commits") or {}), "latest": str(d.get("latest") or "")}
    except (OSError, ValueError, AttributeError):
        return {"commits": {}, "latest": ""}


RELEASES = load_releases()
VER_RE = re.compile(r"^[0-9.]+-t([0-9a-f]{7,40})")


def vkey(v):
    return tuple(int(x) for x in v.split(".") if x.isdigit())


def app_version(raw):
    """What a device's reported version (e.g. 1.105.0-tfd7dc84b3) means.

    TapperVPN builds carry the git commit they were built from; that commit's VERSION is the
    TapperVPN release. Other clients (a GL.iNet router's stock Tailscale) show as reported.
    """
    if not raw:
        return {"raw": "", "release": "", "status": "unknown"}
    m = VER_RE.match(raw)
    rel = ""
    if m:
        h = m.group(1)
        rel = next((v for c, v in RELEASES["commits"].items() if c.startswith(h)), "")
    latest = RELEASES["latest"]
    if not rel:
        status = "other"
    elif latest and vkey(rel) < vkey(latest):
        status = "old"
    else:
        status = "current"
    return {"raw": raw, "release": rel, "status": status}


def diag_versions():
    try:
        with open(DIAG_STATE) as f:
            return json.load(f).get("versions") or {}
    except (OSError, ValueError, AttributeError):
        return {}


def node_view(n, versions=None):
    approved = set(n.get("approvedRoutes") or [])
    available = set(n.get("availableRoutes") or [])
    ips = n.get("ipAddresses") or []
    v4 = next((i for i in ips if "." in i), "")
    info = (versions or {}).get(v4) or {}
    return {
        "app": dict(app_version(info.get("version", "")), os=info.get("os", ""),
                    model=info.get("model", "")),
        "id": n["id"],
        "name": n.get("givenName") or n.get("name"),
        "hostname": n.get("name"),
        "user": (n.get("user") or {}).get("name"),
        "ips": n.get("ipAddresses") or [],
        "online": bool(n.get("online")),
        "lastSeen": n.get("lastSeen"),
        "createdAt": n.get("createdAt"),
        "expiry": n.get("expiry"),
        "exitOffered": EXIT_ROUTES <= available,
        "exitApproved": EXIT_ROUTES <= approved,
        "subnetRoutes": sorted(r for r in available if r not in EXIT_ROUTES),
        "approvedSubnets": sorted(r for r in approved if r not in EXIT_ROUTES),
        "tags": n.get("tags") or [],
        "workstation": WS_TAG in (n.get("tags") or []),
        "server": SERVER_TAG in (n.get("tags") or []),
        "router": router_view(n["id"]),
    }


WS_TAG, SERVER_TAG, DEVICE_TAG = "tag:workstation", "tag:server", "tag:device"


def set_workstation(nid, on):
    """Swap between the workstation tag and the plain device tag, keeping the device's name.

    headscale won't let a tagged device have zero tags, so "off" means tag:device (no extra
    access) rather than no tag. Tagging also renames a device, so the name is put back.
    """
    code, d = hs("GET", f"/api/v1/node/{nid}")
    if code != 200:
        return code, d
    node = d["node"]
    if SERVER_TAG in (node.get("tags") or []):
        return 400, {"error": "That's the server itself."}
    name = node.get("givenName")
    tags = set(node.get("tags") or [])
    if (WS_TAG in tags) == on:
        return 200, {"ok": True}  # already that way; don't tag a device that was never tagged
    tags = (tags - {DEVICE_TAG}) | {WS_TAG} if on else (tags - {WS_TAG}) | {DEVICE_TAG}
    code, d = hs("POST", f"/api/v1/node/{nid}/tags", {"tags": sorted(tags)})
    if code != 200:
        return code, d
    if name and d.get("node", {}).get("givenName") != name:
        hs("POST", f"/api/v1/node/{nid}/rename/{urllib.parse.quote(name)}")
    return 200, {"ok": True}


def key_view(k):
    now = datetime.now(timezone.utc)
    exp = k.get("expiration")
    try:
        exp_dt = datetime.fromisoformat(exp.replace("Z", "+00:00")) if exp else None
    except ValueError:
        exp_dt = None
    if exp_dt and exp_dt <= now:
        status = "expired"
    elif k.get("used") and not k.get("reusable"):
        status = "used"
    else:
        status = "active"
    return {
        "id": k["id"],
        "prefix": str(k.get("key", ""))[:18],
        "reusable": bool(k.get("reusable")),
        "used": bool(k.get("used")),
        "expiration": exp,
        "createdAt": k.get("createdAt"),
        "status": status,
    }


PENDING_MAX_AGE = timedelta(minutes=15)  # headscale's default registration cache lifetime
PENDING_RE = re.compile(r"^(\S+)\s+\S+\s+(starting|new followup) node registration using auth id: (hskey-authreq-[A-Za-z0-9_-]+)")
handled = {}  # auth id -> time we approved/rejected it, so it drops off the list


def pending_logins():
    """Login requests waiting for approval, newest first, read from headscale's log.

    headscale has no API to list them; it logs each one as it arrives. A request can also be
    finished elsewhere (the CLI, or the device giving up); those drop off after 15 minutes.
    """
    import subprocess
    now = datetime.now(timezone.utc)
    for k, t in list(handled.items()):
        if now - t > PENDING_MAX_AGE:
            del handled[k]
    try:
        out = subprocess.run(
            ["journalctl", "-u", "headscale", "--since", "-16min", "--no-pager", "-o", "cat"],
            capture_output=True, text=True, timeout=10,
        ).stdout
    except Exception:
        return []
    found = {}
    for line in out.splitlines():
        m = PENDING_RE.match(line)
        if not m:
            continue
        try:
            at = datetime.fromisoformat(m.group(1)).astimezone(timezone.utc)
        except ValueError:
            continue
        code = m.group(3)
        if now - at > PENDING_MAX_AGE or code in handled:
            continue
        found[code] = {"code": code, "tail": code[-4:], "at": at.isoformat(),
                       "retry": m.group(2) == "new followup"}
    return sorted(found.values(), key=lambda p: p["at"], reverse=True)


# --- diagnostics (data from vpn-diag, the root collector; see server/vpn-diag) ---
DIAG_STATE = "/run/vpn-diag/state.json"
DIAG_EVENTS = "/var/lib/vpn-diag/events.jsonl"
SPEED_MAX = 100 * 1024 * 1024
speed_results = []  # recent speed tests, newest last (kept in memory)
crash_reports = []  # recent app crash reports, newest last (kept in memory; also in the journal)
_chunk = os.urandom(256 * 1024)  # random, so nothing on the way can compress it


def diag(device):
    try:
        with open(DIAG_STATE) as f:
            st = json.load(f)
    except (OSError, ValueError):
        return {"error": "The diagnostics collector (vpn-diag) isn't running.", "devices": [], "flows": [], "events": []}
    try:
        with open(DIAG_EVENTS) as f:
            lines = f.readlines()[-1500:]
        events = [json.loads(l) for l in lines if l.strip()]
    except (OSError, ValueError):
        events = []
    events += speed_results + crash_reports
    if device:
        st["devices"] = [d for d in st["devices"] if d["ip"] == device]
        st["flows"] = [f for f in st["flows"] if f["src"] == device]
        events = [e for e in events if e.get("device") == device]
    events.sort(key=lambda e: e.get("at", ""), reverse=True)
    st["events"] = events[:300]
    return st


# --- routers (GL.iNet package): they check in every minute, see glinet/.../checkin ---
ROUTER_CMDS = ("update", "check", "reapply")
ROUTER_FIELDS = ("version", "arch", "model", "firmware", "uptime", "mode", "exit_node", "auto_update",
                 "busy", "last_action", "last_at", "last_ok", "last_result", "waiting")
routers = {}         # node id -> last check-in report (+ "seen", "ip")
router_cmds = {}     # node id -> {"do": command, "at": when asked}
_node_cache = {"at": None, "by_ip": {}}


def node_for_ip(ip):
    """The headscale node that has this VPN address (cached 30 s)."""
    now = datetime.now(timezone.utc)
    if not _node_cache["at"] or now - _node_cache["at"] > timedelta(seconds=30) or ip not in _node_cache["by_ip"]:
        code, d = hs("GET", "/api/v1/node")
        if code == 200:
            _node_cache["by_ip"] = {a: str(n["id"]) for n in d.get("nodes", []) for a in (n.get("ipAddresses") or [])}
            _node_cache["at"] = now
    return _node_cache["by_ip"].get(ip)


def router_view(nid):
    r = routers.get(str(nid))
    if not r:
        return None
    latest = RELEASES["latest"]
    ver = r.get("version", "")
    pending = router_cmds.get(str(nid))
    return {
        **{k: r.get(k, "") for k in ROUTER_FIELDS},
        "seen": r["seen"],
        "stale": datetime.now(timezone.utc) - datetime.fromisoformat(r["seen"]) > timedelta(minutes=3),
        "outOfDate": bool(latest and ver and vkey(ver.split("-")[0]) < vkey(latest)),
        "latest": latest,
        "pending": pending["do"] if pending else "",
        "pendingAt": pending["at"] if pending else "",
    }


def user_id(name):
    code, d = hs("GET", "/api/v1/user")
    if code != 200:
        return None
    for u in d.get("users", []):
        if u.get("name") == name:
            return u["id"]
    return None


class Handler(BaseHTTPRequestHandler):
    server_version = "vpn-admin"

    def log_message(self, fmt, *args):
        print("%s %s" % (self.client_address[0], fmt % args), flush=True)

    # --- access control -------------------------------------------------
    def allowed(self):
        if self.headers.get("Cf-Connecting-IP") or self.headers.get("Cf-Ray"):
            return False  # came through the Cloudflare tunnel
        try:
            ip = ipaddress.ip_address(self.client_address[0].split("%")[0])
        except ValueError:
            return False
        if getattr(ip, "ipv4_mapped", None):
            ip = ip.ipv4_mapped
        return any(ip in net for net in ALLOWED_NETS)

    def send_json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def deny(self):
        self.send_json(403, {"error": "Only available at home or over the VPN."})

    def router_checkin(self):
        """A router's once-a-minute report (form-encoded, from glinet's checkin script).

        Only over the VPN: the sender's VPN address says which router it is (WireGuard
        addresses can't be faked). The reply is one line: a command, or "none".
        """
        ip = self.client_address[0].split("%")[0].removeprefix("::ffff:")
        nid = node_for_ip(ip) if any(ipaddress.ip_address(ip) in net for net in VPN_NETS) else None
        if not nid:
            body, code = b"unknown\n", 403
        else:
            try:
                n = min(int(self.headers.get("Content-Length") or 0), 4096)
                form = urllib.parse.parse_qs(self.rfile.read(n).decode(errors="replace"))
            except ValueError:
                form = {}
            report = {k: (form.get(k) or [""])[0][:200] for k in ROUTER_FIELDS}
            report.update(seen=datetime.now(timezone.utc).isoformat(timespec="seconds"), ip=ip)
            routers[nid] = report
            cmd = router_cmds.pop(nid, None)
            if cmd and report.get("busy") == "1":
                router_cmds[nid] = cmd          # still busy with the last one: ask next time
                cmd = None
            if cmd:
                print(f"router {ip} (node {nid}): sent {cmd['do']}", flush=True)
            body, code = ((cmd["do"] if cmd else "none") + "\n").encode(), 200
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    # --- routes -----------------------------------------------------------
    def do_GET(self):
        if not self.allowed():
            return self.deny()
        path = urllib.parse.urlparse(self.path).path
        if path in ("/", "/index.html"):
            with open(os.path.join(HERE, "index.html"), "rb") as f:
                body = f.read()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Frame-Options", "DENY")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif path == "/api/nodes":
            code, d = hs("GET", "/api/v1/node")
            if code != 200:
                return self.send_json(code, d)
            vers = diag_versions()
            nodes = sorted((node_view(n, vers) for n in d.get("nodes", [])),
                           key=lambda n: (not n["online"], n["name"].lower()))
            self.send_json(200, {"nodes": nodes, "latest": RELEASES["latest"]})
        elif path == "/api/pending":
            self.send_json(200, {"pending": pending_logins()})
        elif path == "/api/diag":
            q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            dev = (q.get("device") or [""])[0]
            if dev and not re.fullmatch(r"[0-9a-f.:]{3,45}", dev):
                return self.send_json(400, {"error": "bad device"})
            self.send_json(200, diag(dev))
        elif path == "/api/whoami":
            ip = self.client_address[0].split("%")[0]
            if ip.startswith("::ffff:"):
                ip = ip[7:]
            self.send_json(200, {"ip": ip})
        elif path == "/api/speed/down":
            # Download test: sends random bytes as fast as the path allows.
            q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            try:
                n = max(1, min(int((q.get("mb") or ["10"])[0]), SPEED_MAX >> 20)) << 20
            except ValueError:
                n = 10 << 20
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(n))
            self.end_headers()
            try:
                while n > 0:
                    c = _chunk[:min(n, len(_chunk))]
                    self.wfile.write(c)
                    n -= len(c)
            except (BrokenPipeError, ConnectionResetError):
                pass
        elif path == "/api/keys":
            code, d = hs("GET", "/api/v1/preauthkey")
            if code != 200:
                return self.send_json(code, d)
            keys = [key_view(k) for k in d.get("preAuthKeys", [])]
            order = {"active": 0, "used": 1, "expired": 2}
            keys.sort(key=lambda k: (order[k["status"]], -int(k["id"])))
            self.send_json(200, {"keys": keys})
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if not self.allowed():
            return self.deny()
        path0 = urllib.parse.urlparse(self.path).path
        if path0 == "/api/speed/up" and self.headers.get("X-VPN-Admin") == "1":
            # Upload test: reads and discards up to 100 MB.
            try:
                n = min(int(self.headers.get("Content-Length") or 0), SPEED_MAX)
            except ValueError:
                n = 0
            while n > 0:
                got = self.rfile.read(min(n, 1 << 20))
                if not got:
                    break
                n -= len(got)
            return self.send_json(200, {"ok": True})
        if path0 == "/api/speed/result" and self.headers.get("X-VPN-Admin") == "1":
            try:
                body = json.loads(self.rfile.read(min(int(self.headers.get("Content-Length") or 0), 512)) or b"{}")
                down, up = float(body.get("down", 0)), float(body.get("up", 0))
            except (ValueError, TypeError):
                return self.send_json(400, {"error": "bad JSON"})
            ip = self.client_address[0].split("%")[0].removeprefix("::ffff:")
            speed_results.append({"at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
                                  "kind": "speed", "device": ip,
                                  "msg": f"Speed test to the server: {down:.1f} Mbit/s down, {up:.1f} Mbit/s up",
                                  "down": down, "up": up})
            del speed_results[:-50]
            return self.send_json(200, {"ok": True})
        if path0 == "/api/router/checkin":
            return self.router_checkin()
        if path0 == "/api/crash" and self.headers.get("X-VPN-Admin") == "1":
            # Crash report from the Android app (CrashReporter.kt): device info + stack trace.
            try:
                n = min(int(self.headers.get("Content-Length") or 0), 64 * 1024)
                report = str(json.loads(self.rfile.read(n) or b"{}").get("report", ""))[:60000]
            except (ValueError, TypeError, AttributeError):
                return self.send_json(400, {"error": "bad JSON"})
            if not report.strip():
                return self.send_json(400, {"error": "empty report"})
            ip = self.client_address[0].split("%")[0].removeprefix("::ffff:")
            headers = ("time:", "version:", "device:", "thread:")
            first = next((l.strip() for l in report.splitlines()
                          if l.strip() and not l.startswith(headers)), "unknown error")
            crash_reports.append({"at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
                                  "kind": "crash", "device": ip,
                                  "msg": "App crashed: " + first[:200], "detail": report})
            del crash_reports[:-20]
            # Also into the journal, so a report survives a restart of this page.
            print(f"crash report from {ip}:\n{report}", flush=True)
            return self.send_json(200, {"ok": True})
        # A custom header + JSON body means another website can't trigger these.
        if self.headers.get("X-VPN-Admin") != "1" or \
                not (self.headers.get("Content-Type") or "").startswith("application/json"):
            return self.send_json(400, {"error": "bad request"})
        try:
            length = min(int(self.headers.get("Content-Length") or 0), 4096)
            body = json.loads(self.rfile.read(length) or b"{}")
        except Exception:
            return self.send_json(400, {"error": "bad JSON"})

        path = urllib.parse.urlparse(self.path).path
        m = re.fullmatch(r"/api/nodes/([0-9]+)/(rename|delete|expire|exit|workstation|router)", path)
        if path == "/api/approve":
            key = str(body.get("code", "")).strip()
            key = key.rsplit("/", 1)[-1]  # allow pasting the whole login link
            if not AUTH_RE.match(key):
                return self.send_json(400, {"error": "That doesn't look like a login code (hskey-authreq-…)."})
            q = urllib.parse.urlencode({"user": DEFAULT_USER, "key": key})
            code, d = hs("POST", "/api/v1/node/register?" + q)
            if code == 200 and "node" in d:
                handled[key] = datetime.now(timezone.utc)
                return self.send_json(200, {"ok": True, "node": node_view(d["node"])})
            if code == 404:
                handled[key] = datetime.now(timezone.utc)  # gone or expired: stop listing it
            return self.send_json(code if code != 200 else 500, d)
        if path == "/api/reject":
            key = str(body.get("code", "")).strip()
            if not AUTH_RE.match(key):
                return self.send_json(400, {"error": "bad code"})
            code, d = hs("POST", "/api/v1/auth/reject", {"authId": key})
            if code in (200, 404):
                handled[key] = datetime.now(timezone.utc)
                return self.send_json(200, {"ok": True})
            return self.send_json(code, d)
        if path == "/api/keys":
            try:
                days = int(body.get("days", 30))
            except (TypeError, ValueError):
                days = 30
            days = max(1, min(days, 365))
            uid = user_id(DEFAULT_USER)
            if not uid:
                return self.send_json(500, {"error": f"user {DEFAULT_USER} not found"})
            exp = (datetime.now(timezone.utc) + timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")
            code, d = hs("POST", "/api/v1/preauthkey", {
                "user": uid, "reusable": bool(body.get("reusable", True)),
                "ephemeral": False, "expiration": exp,
            })
            if code == 200 and "preAuthKey" in d:
                k = d["preAuthKey"]
                # The full key is returned only here, once, to the browser that made it.
                return self.send_json(200, {"ok": True, "key": k.get("key"), "info": key_view(k)})
            return self.send_json(code if code != 200 else 500, d)
        km = re.fullmatch(r"/api/keys/([0-9]{1,10})/expire", path)
        if km:
            code, d = hs("POST", "/api/v1/preauthkey/expire", {"id": km.group(1)})
            return self.send_json(200, {"ok": True}) if code == 200 else self.send_json(code, d)
        if not m or not ID_RE.match(m.group(1)):
            return self.send_json(404, {"error": "not found"})

        nid, action = m.group(1), m.group(2)
        if action == "router":
            do = str(body.get("do", ""))
            if do == "cancel":
                router_cmds.pop(nid, None)
                return self.send_json(200, {"ok": True})
            if do not in ROUTER_CMDS:
                return self.send_json(400, {"error": "unknown router action"})
            if nid not in routers:
                return self.send_json(400, {"error": "This device hasn't checked in as a TapperVPN router."})
            router_cmds[nid] = {"do": do, "at": datetime.now(timezone.utc).isoformat(timespec="seconds")}
            return self.send_json(200, {"ok": True})
        if action == "rename":
            name = str(body.get("name", "")).strip().lower()
            if not NAME_RE.match(name):
                return self.send_json(400, {"error": "Use lowercase letters, numbers and dashes."})
            code, d = hs("POST", f"/api/v1/node/{nid}/rename/{urllib.parse.quote(name)}")
        elif action == "delete":
            code, d = hs("DELETE", f"/api/v1/node/{nid}")
        elif action == "workstation":
            code, d = set_workstation(nid, bool(body.get("on")))
        elif action == "expire":
            code, d = hs("POST", f"/api/v1/node/{nid}/expire")
        else:  # exit node on/off
            code, d = hs("GET", f"/api/v1/node/{nid}")
            if code != 200:
                return self.send_json(code, d)
            approved = set(d["node"].get("approvedRoutes") or [])
            approved = (approved | EXIT_ROUTES) if body.get("on") else (approved - EXIT_ROUTES)
            code, d = hs("POST", f"/api/v1/node/{nid}/approve_routes", {"routes": sorted(approved)})
        if code == 200:
            return self.send_json(200, {"ok": True})
        self.send_json(code, d)


def main():
    api_key()  # fail fast if the key is missing
    srv = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(f"vpn-admin on :{PORT}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
