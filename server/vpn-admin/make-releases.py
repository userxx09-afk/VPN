#!/usr/bin/env python3
"""Writes releases.json for the admin page: each release commit and its TapperVPN version.

Apps report the commit they were built from (e.g. 1.105.0-tfd7dc84b3); each commit's
VERSION file names the release it belongs to.
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
    # Every commit maps to the VERSION in effect there, not just the commits that changed
    # VERSION: a build from a later fix-up commit (e.g. a workflow tweak after "Release
    # 0.2.15") is still that release.
    hashes = git("log", "--format=%H").split()
    batch = subprocess.run(["git", "-C", ROOT, "cat-file", "--batch"], input="".join(
        f"{h}:VERSION\n" for h in hashes), capture_output=True, text=True, check=True).stdout
    commits, pos = {}, 0
    for h in hashes:
        header = batch[pos:batch.index("\n", pos)]
        pos += len(header) + 1
        if header.endswith("missing"):
            continue  # no VERSION file at that commit
        size = int(header.split()[2])
        v = batch[pos:pos + size].strip()
        pos += size + 1
        if v:
            commits[h] = v
    latest = git("show", "HEAD:VERSION").strip()
    with open(out, "w") as f:
        json.dump({"latest": latest, "commits": commits}, f, indent=1)
    print(f"{out}: {len(commits)} commits, latest {latest}")


if __name__ == "__main__":
    main()
