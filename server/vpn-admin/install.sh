#!/bin/sh
# Installs or updates the admin page (vpn-admin, :8096) and its diagnostics
# collector (vpn-diag) on the server. Run as root from a checkout of this repo:
#
#   sudo sh server/vpn-admin/install.sh
#
# The admin page also needs a headscale API key in /etc/vpn-admin/api-key
# (once: headscale -c /etc/headscale/config.yaml apikeys create --expiration 999d).
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
DIAG=$HERE/../vpn-diag

# conntrack lists connections through the server; byte counts need accounting on.
command -v conntrack >/dev/null || apt-get install -y conntrack >/dev/null
echo net.netfilter.nf_conntrack_acct=1 > /etc/sysctl.d/60-vpn-diag.conf
sysctl -q -p /etc/sysctl.d/60-vpn-diag.conf || true

install -d -m 0755 /opt/vpn-admin /opt/vpn-diag
install -m 0644 "$HERE/server.py" "$HERE/index.html" /opt/vpn-admin/
install -m 0644 "$DIAG/collector.py" /opt/vpn-diag/
install -m 0644 "$HERE/vpn-admin.service" "$DIAG/vpn-diag.service" /etc/systemd/system/
[ -f /etc/vpn-admin/api-key ] || echo "Add /etc/vpn-admin/api-key (see the top of this script)." >&2

systemctl daemon-reload
systemctl enable --now vpn-diag vpn-admin
systemctl restart vpn-diag vpn-admin
sleep 2
systemctl is-active vpn-diag vpn-admin
