#!/bin/bash

set -eu

# Clean up folders and files created during build.
function cleanup() {
	rm -rf /TapperVPN/$ARCH
	rm -f /TapperVPN/sed*
	rm -f /TapperVPN/qpkg.cfg
}
trap cleanup EXIT

mkdir -p /TapperVPN/$ARCH
cp /tappervpnd /TapperVPN/$ARCH/tappervpnd
cp /tappervpn /TapperVPN/$ARCH/tappervpn

sed "s/\$QPKG_VER/$TSTAG-$QNAPTAG/g" /TapperVPN/qpkg.cfg.in >/TapperVPN/qpkg.cfg

qbuild --root /TapperVPN --build-arch $ARCH --build-dir /out
