#!/bin/sh
# Builds the Fire TV (Vega OS) app on Ubuntu and, optionally, installs it on
# a Fire TV in Developer Mode. Needs Node 22+ and Amazon's Vega SDK
# (see tappervpn-vega/README.md, "Ubuntu build machine").
#
#   scripts/build-vega.sh                 # tests + build; prints the .vpkg path
#   scripts/build-vega.sh --install       # ...and install + launch on the connected Fire TV
#   VEGA_SERIAL=192.168.1.50:5555 scripts/build-vega.sh --install   # a TV over the network
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
APP=$ROOT/tappervpn-vega
ARCH=${VEGA_ARCH:-armv7}   # Fire TV sticks; x86_64 for the Vega Virtual Device

[ -f "$HOME/vega/env" ] && . "$HOME/vega/env"
command -v vega >/dev/null 2>&1 || {
	echo "Vega CLI not found. Install the SDK: curl -fsSL https://sdk-installer.vega.labcollab.net/get_vvm.sh | bash && . ~/vega/env" >&2
	exit 1
}
vega --version

cd "$APP"
npm ci --no-audit --no-fund
npm run typecheck
npm test
npm run build:app

VPKG=$(ls "build/$ARCH-release/"*.vpkg 2>/dev/null | head -1 || true)
[ -n "$VPKG" ] || { echo "No $ARCH release .vpkg under $APP/build (found: $(ls build))" >&2; exit 1; }
echo "Built $APP/$VPKG"

if [ "${1:-}" = "--install" ]; then
	if [ -n "${VEGA_SERIAL:-}" ]; then
		vega exec vda connect "$VEGA_SERIAL" || true
	fi
	vega device install-app --packagePath "$VPKG"
	vega device launch-app --appName com.tappervpn.vega.main
fi
