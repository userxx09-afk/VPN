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

# The server can also be a node of its own network (e.g. an exit node). Its
# agent runs as tappervpnd; join it with server/join-exit-node.sh.
[ -f /etc/default/tappervpnd ] || cat >/etc/default/tappervpnd <<'EOF'
# UDP port for VPN traffic (forward it on the router for direct connections).
PORT="41641"
# Extra flags for tappervpnd.
FLAGS=""
EOF

cat >/etc/systemd/system/tappervpnd.service <<'EOF'
[Unit]
Description=TapperVPN node agent
Wants=network-pre.target
After=network-pre.target NetworkManager.service systemd-resolved.service headscale.service

[Service]
EnvironmentFile=/etc/default/tappervpnd
ExecStart=/usr/local/bin/tappervpnd --state=/var/lib/tappervpn/tappervpnd.state --socket=/run/tappervpn/tappervpnd.sock --port=${PORT} $FLAGS
ExecStopPost=/usr/local/bin/tappervpnd --cleanup
Restart=on-failure
RuntimeDirectory=tappervpn
RuntimeDirectoryMode=0755
StateDirectory=tappervpn
StateDirectoryMode=0700
CacheDirectory=tappervpn
CacheDirectoryMode=0750
Type=notify

[Install]
WantedBy=multi-user.target
EOF

# Announce the control server on the local network (mDNS / DNS-SD), so
# clients set up for the public name use this server directly when they're
# on the same network. Clients only accept a server holding the control key
# built into them, so every standby must use the same noise_private.key.
if [ -d /etc/avahi ] || apt-get install -y avahi-daemon >/dev/null 2>&1; then
	install -d -m 0755 /etc/avahi/services
	cat > /etc/avahi/services/tappervpn.service <<'EOF'
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">TapperVPN control on %h</name>
  <service protocol="ipv4">
    <type>_tappervpn._tcp</type>
    <port>8080</port>
    <txt-record>path=/</txt-record>
  </service>
</service-group>
EOF
	systemctl enable --now avahi-daemon >/dev/null 2>&1 || true
	systemctl reload avahi-daemon 2>/dev/null || true
else
	echo "Couldn't install avahi-daemon; clients won't find this server on the local network." >&2
fi

systemctl daemon-reload
systemctl enable --now headscale tappervpn-update.timer
systemctl restart headscale
sleep 3
systemctl is-active --quiet headscale && echo "headscale is running." || { echo "headscale didn't start: journalctl -u headscale" >&2; exit 1; }
/usr/local/bin/tappervpn-updater -check || true
echo "Daily updates: systemctl list-timers tappervpn-update.timer"
