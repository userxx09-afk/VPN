#!/usr/bin/env python3
"""Builds TapperVPN packages (.ipk) for GL.iNet / OpenWrt routers.

    python glinet/build.py              # build binaries, then packages
    python glinet/build.py --skip-go    # reuse bin/glinet/tappervpnd-* binaries

Output: bin/glinet/tappervpn_<version>_<arch>.ipk, one per router CPU.
Install through LuCI (System > Software > Upload Package) or with
`opkg install /tmp/tappervpn_*.ipk` over SSH.

The packages are Architecture "all" so opkg accepts them on any target; the
postinst checks the binary actually runs, since opkg can't tell CPUs apart
for a static Go binary.
"""

import argparse
import gzip
import io
import os
import subprocess
import sys
import tarfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TAILSCALE = os.path.join(ROOT, "tailscale")
FILES = os.path.join(ROOT, "glinet", "files")
OUT = os.path.join(ROOT, "bin", "glinet")
VERSION = "0.1.0"

# name -> (GOARCH, extra env, CPUs it is for)
TARGETS = {
    "aarch64": ("arm64", {}, "Flint 2 (MT6000), Beryl AX (MT3000), Slate AX (AXT1800), Flint (AX1800), Brume 2"),
    "armv7": ("arm", {"GOARM": "7"}, "older ARM models"),
    "mipsel": ("mipsle", {"GOMIPS": "softfloat"}, "MediaTek MIPS models with 128 MB+ flash"),
    "mips": ("mips", {"GOMIPS": "softfloat"}, "Atheros/Qualcomm MIPS models with 128 MB+ flash"),
}

# Only what a router needs; keeps the binary around 17 MB.
FEATURES = (
    "cli,osrouter,nattraversal,udptransport,useexitnode,useroutes,"
    "advertiseexitnode,advertiseroutes,dns,peerapiclient,peerapiserver,"
    "netstack,portmapper,ipnbus,health,gro,bakedroots,listenrawdisco"
)

EXECUTABLES = {
    "etc/init.d/tappervpn",
    "usr/sbin/tappervpn",
    "usr/sbin/tappervpnd",
    "usr/libexec/tappervpn/apply",
}

POSTINST = """#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
if ! /usr/sbin/tappervpnd --version >/dev/null 2>&1; then
\techo "TapperVPN: this package is for a different router CPU. Remove it and install the right one." >&2
\texit 1
fi
if pidof tailscaled >/dev/null 2>&1; then
\techo "TapperVPN: the router's built-in Tailscale is running. Turn it off; the two can't run together." >&2
fi
rm -rf /tmp/luci-indexcache* /tmp/luci-modulecache 2>/dev/null
/etc/init.d/rpcd reload 2>/dev/null
/etc/init.d/tappervpn enable
/etc/init.d/tappervpn restart
exit 0
"""

PRERM = """#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
/etc/init.d/tappervpn stop
/etc/init.d/tappervpn disable
exit 0
"""


def git_hash():
    try:
        return subprocess.check_output(
            ["git", "-C", TAILSCALE, "rev-parse", "--short", "HEAD"], text=True
        ).strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


def build_binaries(names):
    tags = subprocess.check_output(
        ["go", "run", "./cmd/featuretags", "--min", "--add=" + FEATURES],
        cwd=TAILSCALE, text=True,
    ).strip()
    os.makedirs(OUT, exist_ok=True)
    for name in names:
        goarch, extra, _ = TARGETS[name]
        env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH=goarch, **extra)
        out = os.path.join(OUT, "tappervpnd-" + name)
        print("building", out, flush=True)
        subprocess.check_call(
            ["go", "build", "-tags=" + tags, "-trimpath", "-ldflags=-s -w", "-o", out, "./cmd/tailscaled"],
            cwd=TAILSCALE, env=env,
        )


def tar_add(tar, name, data, mode):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = int(time.time())
    info.uid = info.gid = 0
    info.uname = info.gname = "root"
    tar.addfile(info, io.BytesIO(data))


def tar_dir(tar, name):
    info = tarfile.TarInfo(name)
    info.type = tarfile.DIRTYPE
    info.mode = 0o755
    info.mtime = int(time.time())
    tar.addfile(info)


def targz(entries):
    """entries: list of (path, bytes or None for a directory, mode)."""
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", mtime=0) as gz:
        with tarfile.open(fileobj=gz, mode="w", format=tarfile.GNU_FORMAT) as tar:
            for path, data, mode in entries:
                if data is None:
                    tar_dir(tar, path)
                else:
                    tar_add(tar, path, data, mode)
    return buf.getvalue()


def read(path):
    with open(path, "rb") as f:
        data = f.read()
    # Scripts and configs are edited on Windows; the router needs LF endings.
    return data.replace(b"\r\n", b"\n") if b"\0" not in data[:1024] else data


def package(name, version):
    binary = os.path.join(OUT, "tappervpnd-" + name)
    files = {}
    for dirpath, _, filenames in os.walk(FILES):
        for fn in filenames:
            full = os.path.join(dirpath, fn)
            rel = os.path.relpath(full, FILES).replace(os.sep, "/")
            files[rel] = read(full)
    files["usr/sbin/tappervpnd"] = read(binary)
    with open(os.path.join(TAILSCALE, "LICENSE"), "rb") as f:
        files["usr/share/doc/tappervpn/LICENSE"] = f.read()

    dirs = set()
    for rel in files:
        parts = rel.split("/")[:-1]
        for i in range(1, len(parts) + 1):
            dirs.add("/".join(parts[:i]))
    entries = [("./", None, 0o755)]
    entries += [("./" + d + "/", None, 0o755) for d in sorted(dirs)]
    entries += [("./" + rel, data, 0o755 if rel in EXECUTABLES else 0o644) for rel, data in sorted(files.items())]
    data_tgz = targz(entries)

    size = sum(len(d) for d in files.values())
    control = (
        "Package: tappervpn\n"
        f"Version: {version}\n"
        "Depends: kmod-tun\n"
        "Section: net\n"
        "Architecture: all\n"
        f"Installed-Size: {size}\n"
        "Maintainer: TapperVPN\n"
        f"Description: TapperVPN mesh VPN client for {name} routers ({TARGETS[name][2]}).\n"
        " Sends chosen LAN devices or websites through an exit node. Settings: LuCI > Services > TapperVPN.\n"
    )
    control_tgz = targz([
        ("./", None, 0o755),
        ("./control", control.encode(), 0o644),
        ("./conffiles", b"/etc/config/tappervpn\n", 0o644),
        ("./postinst", POSTINST.encode(), 0o755),
        ("./prerm", PRERM.encode(), 0o755),
    ])

    ipk = targz([
        ("./debian-binary", b"2.0\n", 0o644),
        ("./data.tar.gz", data_tgz, 0o644),
        ("./control.tar.gz", control_tgz, 0o644),
    ])
    out = os.path.join(OUT, f"tappervpn_{version}_{name}.ipk")
    with open(out, "wb") as f:
        f.write(ipk)
    print(f"wrote {out} ({len(ipk) // 1024} KB)")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--skip-go", action="store_true", help="reuse existing binaries")
    ap.add_argument("targets", nargs="*", default=list(TARGETS), help=" ".join(TARGETS))
    args = ap.parse_args()
    for t in args.targets:
        if t not in TARGETS:
            sys.exit(f"unknown target {t}; choose from {' '.join(TARGETS)}")
    if not args.skip_go:
        build_binaries(args.targets)
    version = f"{VERSION}-{git_hash()}"
    for t in args.targets:
        package(t, version)


if __name__ == "__main__":
    main()
