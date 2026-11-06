#!/bin/sh
# Makes the control server a node of its own network and offers it as an
# exit node (devices can send all their internet traffic through it).
# Run as root on the server after install.sh (needs jq: apt install jq):
#
#   sudo sh join-exit-node.sh [user] [hostname]     # defaults: admin, tapper
set -eu

USER_NAME=${1:-admin}
NODE_NAME=${2:-tapper}
# The control server runs on this machine, so talk to it locally: that
# address never changes when the machine's network address does.
PORT=$(sed -n 's/^listen_addr:.*:\([0-9][0-9]*\).*$/\1/p' /etc/headscale/config.yaml | head -1)
SERVER=${SERVER:-http://127.0.0.1:${PORT:-8080}}

# Exit nodes forward traffic for other devices.
cat >/etc/sysctl.d/99-tappervpn-forwarding.conf <<'EOF'
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
EOF
sysctl -q -p /etc/sysctl.d/99-tappervpn-forwarding.conf

systemctl enable --now tappervpnd
sleep 2

# A one-time key, valid for 10 minutes, so the server needs no browser sign-in.
KEY=$(headscale preauthkeys create --user "$USER_NAME" --expiration 10m -o json | jq -r .key)
[ -n "$KEY" ] && [ "$KEY" != null ] || { echo "couldn't create a key for user $USER_NAME" >&2; exit 1; }

tappervpn up --login-server "$SERVER" --auth-key "$KEY" --hostname "$NODE_NAME" \
	--advertise-exit-node --accept-dns=false --reset --force-reauth

# Approve the exit routes (0.0.0.0/0 and ::/0) the node just advertised.
sleep 3
ID=$(headscale nodes list -o json | jq -r --arg n "$NODE_NAME" '[.[] | select(.givenName == $n)][0].id // empty')
[ -n "$ID" ] || { echo "node $NODE_NAME not found; approve with: headscale nodes approve-routes" >&2; exit 1; }
headscale nodes approve-routes --identifier "$ID" --routes 0.0.0.0/0,::/0
tappervpn status
echo "$NODE_NAME is an exit node. On a device, pick it under Exit node."
