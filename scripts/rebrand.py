#!/usr/bin/env python3
"""Renames Tailscale to TapperVPN in a source tree.

    python scripts/rebrand.py <dir>            # rename in place
    python scripts/rebrand.py <dir> --check    # list what would change

The TapperVPN code in this repo (tappervpn/, headscale/, tappervpn-android/)
has already been through this; edit it directly. The script is for bringing
in a new upstream release: check out upstream tailscale, headscale or
tailscale-android at the new version, run this on it, and merge the result
into our folder (see docs/upstream.md).

What changes:
  - The Go module tailscale.com becomes tappervpn.com, and
    github.com/tailscale/tailscale-android becomes tappervpn.com/android.
  - Every tailscale / Tailscale / TAILSCALE becomes tappervpn / TapperVPN /
    TAPPERVPN: identifiers, binaries (tappervpn, tappervpnd), services, paths,
    UI text, Java/Kotlin packages (com.tappervpn.ipn). File and folder names too.

What stays, so the code still builds and talks to the outside world:
  - Copyright and license notices (BSD-3 requires keeping them).
  - Other modules Tailscale publishes (github.com/tailscale/wireguard-go, ...)
    and tailscale.com/client/tailscale/v2: we use them, we don't fork them.
  - Real host names and URLs (https://tailscale.com/kb/..., derp servers,
    capability URLs such as https://tailscale.com/cap/..., *.tailscale.com).
  - clientupdate/: upstream's updater for Tailscale's own package repos. Only
    its imports change; TapperVPN disables it (our updater is tappervpn/updater).
"""

import argparse
import os
import re
import sys

# Files left exactly as upstream wrote them.
SKIP_NAMES = re.compile(r"^(LICENSE|COPYING|NOTICE|PATENTS|AUTHORS)(\..*)?$", re.I)
SKIP_SUFFIXES = (".sum",)  # go.sum: checksums of other people's modules
SKIP_DIRS = {".git", "node_modules", "build", ".gradle"}

# Folders where only Go import paths change (see docstring).
IMPORTS_ONLY = ("clientupdate/",)

# Masked before renaming, restored after.
PROTECT = [
    re.compile(r"Tailscale Inc"),                           # copyright holder
    re.compile(r"tailscale\.com/client/tailscale/v2"),      # separate module
    re.compile(r"github\.com/tailscale/(?!tailscale-android\b)[\w.-]+"),
    re.compile(r"[\w.-]*@tailscale\.(?:com|io)"),           # email addresses
    re.compile(r"(?:https?:)?//(?:[\w-]+\.)*tailscale\.(?:com|io|net)[^\s\"'`)>\]]*"),
    re.compile(r"\b(?:[\w-]+\.)+tailscale\.(?:com|io|net)\b"),  # login.tailscale.com, ...
    re.compile(r"(?<![\w-])\.tailscale\.(?:com|io|net)\b"),     # ".tailscale.com" suffixes
    re.compile(r"\btailscale\.(?:io|net)\b"),               # domains, never module paths
    re.compile(r'\bh != "tailscale\.com"'),                 # ipnlocal: the web site's host
    re.compile(r"\bts\.net\b"),
    re.compile(r"\bclientupdate\.\w+"),                    # names in the imports-only package
    # Wire protocol with servers Tailscale runs (public DERP relays answer
    # /generate_204 captive-portal checks with these headers).
    re.compile(r"X-Tailscale-(?:Challenge|Response)"),
]

MODULE_RENAMES = [
    (re.compile(r"github\.com/tailscale/tailscale-android"), "tappervpn.com/android"),
    # The module path, not the web site: not preceded by "//", "." or a word char.
    (re.compile(r"(?<![\w./@:-])tailscale\.com(?![\w.-])"), "tappervpn.com"),
]

WORD_RENAMES = [
    (re.compile("TAILSCALE"), "TAPPERVPN"),
    (re.compile("Tailscale"), "TapperVPN"),
    (re.compile("TailScale"), "TapperVPN"),
    (re.compile("tailscale"), "tappervpn"),
    (re.compile("tailscale", re.I), "tappervpn"),  # odd casings, e.g. in tests
]

MASK = "\x00MASK{}\x00"

# The v2 API client keeps its import path, but its package name ("tailscale")
# gets renamed at every use; alias the import to match.
V2_IMPORT = re.compile(r'^(\s*)"tailscale\.com/client/tailscale/v2"', re.M)


def rebrand_text(text, imports_only=False):
    saved = []

    def mask(m):
        saved.append(m.group(0))
        return MASK.format(len(saved) - 1)

    for pat in PROTECT:
        text = pat.sub(mask, text)
    for pat, repl in MODULE_RENAMES + ([] if imports_only else WORD_RENAMES):
        text = pat.sub(repl, text)
    text = re.sub("\x00MASK(\\d+)\x00", lambda m: saved[int(m.group(1))], text)
    if imports_only:
        return text
    return V2_IMPORT.sub(r'\1tappervpn "tailscale.com/client/tailscale/v2"', text)


def rebrand_path(rel):
    if rel.startswith(IMPORTS_ONLY):
        return rel
    for pat, repl in WORD_RENAMES:
        rel = pat.sub(repl, rel)
    return rel


def rebrand_file(full, rel):
    """Returns the new text of the file at full, or None if it doesn't change."""
    base = os.path.basename(rel)
    if SKIP_NAMES.match(base) or base.endswith(SKIP_SUFFIXES):
        return None
    with open(full, "rb") as f:
        data = f.read()
    if b"\0" in data[:8192]:
        return None  # binary
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    new = rebrand_text(text, imports_only=rel.startswith(IMPORTS_ONLY))
    return None if new == text else new


def walk(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            full = os.path.join(dirpath, fn)
            if not os.path.islink(full):
                yield full, os.path.relpath(full, root).replace(os.sep, "/")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("dir", help="source tree to rename in place")
    ap.add_argument("--check", action="store_true", help="only list the changes")
    args = ap.parse_args()
    if not os.path.isdir(args.dir):
        sys.exit(f"{args.dir} is not a folder")
    edited = renamed = 0
    for full, rel in list(walk(args.dir)):
        new = rebrand_file(full, rel)
        if new is not None:
            edited += 1
            if args.check:
                print("edit  ", rel)
            else:
                with open(full, "wb") as f:
                    f.write(new.encode("utf-8"))
        new_rel = rebrand_path(rel)
        if new_rel != rel:
            renamed += 1
            if args.check:
                print("rename", rel, "->", new_rel)
            else:
                dest = os.path.join(args.dir, new_rel)
                os.makedirs(os.path.dirname(dest), exist_ok=True)
                os.replace(full, dest)
    if not args.check:
        for dirpath, _, _ in sorted(os.walk(args.dir), key=lambda w: -len(w[0])):
            if dirpath != args.dir and not os.listdir(dirpath):
                os.rmdir(dirpath)
    print(f"{edited} files edited, {renamed} renamed")


if __name__ == "__main__":
    main()
