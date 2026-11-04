#!/usr/bin/env bash

source tappervpn.version || echo >&2 "no tappervpn.version file found"
if [[ -z "${VERSION_LONG}" ]]; then
    exit 1
fi
echo "-X tappervpn.com/version.longStamp=${VERSION_LONG}"
echo "-X tappervpn.com/version.shortStamp=${VERSION_SHORT}"
echo "-X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}"
echo "-X tappervpn.com/version.extraGitCommitStamp=${VERSION_EXTRA_HASH}"
