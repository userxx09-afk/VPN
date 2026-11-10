#!/usr/bin/env python3
"""Writes releases.json for the admin page: each release commit and its TapperVPN version.

Apps report the commit they were built from (e.g. 1.105.0-tfd7dc84b3); a release is
built from the commit that set VERSION, so that commit names the release.
Run from a git checkout of this repo:  python3 server/vpn-admin/make-releases.py [out]
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, "..", ".."))


def git(*args):
    return subprocess.run(["git", "-C", ROOT, *args], capture_output=True, text=True, check=True).stdout


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "releases.json")
    commits = {}
    for h in git("log", "--format=%H", "--", "VERSION").split():
        try:
            v = git("show", f"{h}:VERSION").strip()
        except subprocess.CalledProcessError:
            continue  # VERSION deleted in that commit
        if v:
            commits[h] = v
    latest = git("show", "HEAD:VERSION").strip()
    with open(out, "w") as f:
        json.dump({"latest": latest, "commits": commits}, f, indent=1)
    print(f"{out}: {len(commits)} releases, latest {latest}")


if __name__ == "__main__":
    main()
