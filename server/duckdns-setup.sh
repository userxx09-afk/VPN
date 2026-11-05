#!/bin/sh
# Keeps a free DuckDNS name (e.g. tappervpn.duckdns.org) pointed at this
# network's public IP, so devices outside can find the control server.
# Run as root on the server, in a terminal (it asks for the token, hidden):
#
#   sudo sh duckdns-setup.sh
#
# Create the name and copy its token at https://www.duckdns.org first.
# Stores them in /etc/tappervpn/duckdns.env (root only) and checks every
# 5 minutes (tappervpn-duckdns.timer).
set -eu

printf "DuckDNS name (the part before .duckdns.org): "
read -r NAME
NAME=$(printf %s "$NAME" | tr 'A-Z' 'a-z' | sed 's/\.duckdns\.org$//; s/[[:space:]]//g')
case "$NAME" in ''|*[!a-z0-9-]*) echo "That doesn't look like a DuckDNS name." >&2; exit 1 ;; esac

printf "DuckDNS token (hidden, press Enter after pasting): "
stty -echo 2>/dev/null || true
read -r TOKEN
stty echo 2>/dev/null || true
echo
TOKEN=$(printf %s "$TOKEN" | tr -d '[:space:]')
[ -n "$TOKEN" ] || { echo "No token pasted; nothing saved." >&2; exit 1; }

install -d -m 0700 /etc/tappervpn
umask 077
printf 'DUCKDNS_NAME=%s\nDUCKDNS_TOKEN=%s\n' "$NAME" "$TOKEN" >/etc/tappervpn/duckdns.env

cat >/usr/local/bin/tappervpn-duckdns <<'EOF'
#!/bin/sh
# Updates the DuckDNS name in /etc/tappervpn/duckdns.env to our public IP.
. /etc/tappervpn/duckdns.env
r=$(curl -fsS --max-time 20 -K - <<CFG
url = "https://www.duckdns.org/update?domains=$DUCKDNS_NAME&token=$DUCKDNS_TOKEN&ip="
CFG
)
[ "$r" = OK ] || { echo "DuckDNS update failed: $r" >&2; exit 1; }
EOF
chmod 0755 /usr/local/bin/tappervpn-duckdns

cat >/etc/systemd/system/tappervpn-duckdns.service <<'EOF'
[Unit]
Description=Point the TapperVPN DuckDNS name at our public IP
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tappervpn-duckdns
EOF

cat >/etc/systemd/system/tappervpn-duckdns.timer <<'EOF'
[Unit]
Description=Keep the TapperVPN DuckDNS name current

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
if /usr/local/bin/tappervpn-duckdns; then
	systemctl enable --now tappervpn-duckdns.timer
	echo "Done: $NAME.duckdns.org now points at this network. Tell Claude 'done'."
else
	echo "DuckDNS rejected the name or token. Check both at duckdns.org and run this again." >&2
	exit 1
fi
