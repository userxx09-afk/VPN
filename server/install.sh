#!/bin/sh
# Installs or upgrades the TapperVPN control server (Headscale) on Ubuntu,
# and turns on daily signed updates from GitHub. Run as root on the server:
#
#   sudo sh install.sh <dir>     # <dir>: an unpacked tappervpn-server-linux-amd64.tar.gz
#
# Keeps an existing /etc/headscale/config.yaml and /var/lib/headscale (the
# database and keys). Run it again any time; it only adds what's missing.
set -eu

SRC=${1:?usage: install.sh <folder with headscale, tappervpn, tappervpnd, tappervpn-updater>}
HERE=$(cd "$(dirname "$0")" && pwd)

for b in headscale tappervpn tappervpnd tappervpn-updater; do
	[ -f "$SRC/$b" ] || { echo "$SRC/$b missing" >&2; exit 1; }
	install -m 0755 "$SRC/$b" "/usr/local/bin/$b.new"
	mv -f "/usr/local/bin/$b.new" "/usr/local/bin/$b"
done

id headscale >/dev/null 2>&1 || useradd --system --home-dir /var/lib/headscale --shell /usr/sbin/nologin headscale
install -d -o headscale -g headscale -m 0750 /var/lib/headscale
install -d -m 0755 /etc/headscale
[ -f /etc/headscale/config.yaml ] || { echo "Add /etc/headscale/config.yaml (see server/config.yaml in the repo)." >&2; }
[ -f /etc/headscale/policy.hujson ] || [ ! -f "$HERE/policy.hujson" ] || install -m 0644 "$HERE/policy.hujson" /etc/headscale/

cat >/etc/systemd/system/headscale.service <<'EOF'
[Unit]
Description=TapperVPN control server (headscale)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=headscale
Group=headscale
ExecStart=/usr/local/bin/headscale serve -c /etc/headscale/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
RuntimeDirectory=headscale
RuntimeDirectoryMode=0750
WorkingDirectory=/var/lib/headscale
ReadWritePaths=/var/lib/headscale
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/tappervpn-update.service <<'EOF'
[Unit]
Description=Install signed TapperVPN updates from GitHub
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tappervpn-updater
EOF

cat >/etc/systemd/system/tappervpn-update.timer <<'EOF'
[Unit]
Description=Daily TapperVPN update check

[Timer]
OnCalendar=*-*-* 04:00
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now headscale tappervpn-update.timer
systemctl restart headscale
sleep 3
systemctl is-active --quiet headscale && echo "headscale is running." || { echo "headscale didn't start: journalctl -u headscale" >&2; exit 1; }
/usr/local/bin/tappervpn-updater -check || true
echo "Daily updates: systemctl list-timers tappervpn-update.timer"
