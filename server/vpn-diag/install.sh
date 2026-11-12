#!/bin/sh
# Installs or updates the diagnostics collector (vpn-diag) on the server. Run as
# root from a checkout of this repo:   sudo sh server/vpn-diag/install.sh
#
# Its results are shown on the Tapper Dashboard (repo userxx09-afk/dashboard),
# which replaced the admin page that used to live in server/vpn-admin.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
# conntrack lists connections through the server; byte counts need accounting on.
command -v conntrack >/dev/null || apt-get install -y conntrack >/dev/null
echo net.netfilter.nf_conntrack_acct=1 > /etc/sysctl.d/60-vpn-diag.conf
sysctl -q -p /etc/sysctl.d/60-vpn-diag.conf || true
install -d -m 0755 /opt/vpn-diag
install -m 0644 "$HERE/collector.py" /opt/vpn-diag/
install -m 0644 "$HERE/vpn-diag.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now vpn-diag
systemctl restart vpn-diag
systemctl is-active vpn-diag
