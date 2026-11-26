#!/bin/sh
# WireGuard bridge for devices that can only run plain WireGuard, such as
# Fire TV on Vega OS (its system VPN is WireGuard only, so it can't run the
# mesh client). Those devices connect to this server with WireGuard, and their
# traffic goes on from here: to the internet through this server's own
# connection, or through the exit node this server uses (tappervpn set
# --exit-node=...), and to the VPN addresses (100.64.0.0/10) of your devices.
#
# Run as root on the control server (needs wireguard-tools, jq, iptables):
#
#   sudo wg-bridge install [public name] [UDP port]   # once; defaults: vp.tapper.co 51821
#   sudo wg-bridge list                               # devices waiting, and paired
#   sudo wg-bridge approve <code> [name]              # the code the TV shows
#   sudo wg-bridge reject <code>
#   sudo wg-bridge remove <name or address>
#   sudo wg-bridge dns <server>[,<server>...]         # DNS for paired devices
#
# Forward the UDP port on the router to this server.
#
# Pairing: the TV makes its own key pair and asks the control server
# (POST /tappervpn/wg/pair) for a 6-digit code, which it shows. approve adds
# the peer here and writes the TV's settings where headscale serves them
# (GET /tappervpn/wg/config). The TV's private key never leaves it.
set -eu

IF=${WG_BRIDGE_IF:-tvpn0}
CONF=/etc/wireguard/$IF.conf
STATE=/etc/tappervpn/wg-bridge
DIR=${WG_BRIDGE_DIR:-/var/lib/headscale/wg-bridge}
SUBNET_PREFIX=10.77.0
MESH_IF=tappervpn0

die() { echo "wg-bridge: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is missing (apt install $2)"; }

# base64 public key -> hex, the file name headscale looks for.
key_hex() { printf %s "$1" | base64 -d 2>/dev/null | od -An -tx1 | tr -d ' \n'; }

server_json() { cat "$STATE/server.json"; }

write_peer_file() { # <pubkey> <name> <address>
	hex=$(key_hex "$1")
	[ ${#hex} -eq 64 ] || die "bad key $1"
	server_json | jq --arg name "$2" --arg addr "$3/32" '{
		name: $name,
		address: $addr,
		dns: .dns,
		mtu: .mtu,
		peer: {
			publicKey: .publicKey,
			endpoint: .endpoint,
			allowedIPs: ["0.0.0.0/0"],
			meshIPs: ["100.64.0.0/10", .subnet],
			persistentKeepalive: 25
		}
	}' >"$DIR/peers/$hex.json.tmp"
	chown headscale:headscale "$DIR/peers/$hex.json.tmp" 2>/dev/null || true
	chmod 0640 "$DIR/peers/$hex.json.tmp"
	mv -f "$DIR/peers/$hex.json.tmp" "$DIR/peers/$hex.json"
}

# Peers are kept in $STATE/peers.tsv: <pubkey> <address> <name>.
sync_wg() {
	{
		cat "$STATE/interface.conf"
		while IFS='	' read -r pub addr name; do
			[ -n "$pub" ] || continue
			printf '\n[Peer]\n# %s\nPublicKey = %s\nAllowedIPs = %s/32\n' "$name" "$pub" "$addr"
		done <"$STATE/peers.tsv"
	} >"$CONF.tmp"
	chmod 0600 "$CONF.tmp"
	mv -f "$CONF.tmp" "$CONF"
	if ip link show "$IF" >/dev/null 2>&1; then
		wg syncconf "$IF" "$(mktemp_strip)"
	fi
}

mktemp_strip() {
	f=$STATE/.strip.conf
	wg-quick strip "$IF" >"$f"
	echo "$f"
}

next_addr() {
	i=2
	while grep -q "	$SUBNET_PREFIX.$i	" "$STATE/peers.tsv" 2>/dev/null; do
		i=$((i + 1))
		[ $i -lt 255 ] || die "no free addresses in $SUBNET_PREFIX.0/24"
	done
	echo "$SUBNET_PREFIX.$i"
}

cmd_install() {
	need wg wireguard-tools
	need jq jq
	need iptables iptables
	HOST=${1:-vp.tapper.co}
	PORT=${2:-51821}

	install -d -m 0700 "$STATE"
	[ -f "$STATE/server.key" ] || (umask 077 && wg genkey >"$STATE/server.key")
	PUB=$(wg pubkey <"$STATE/server.key")
	[ ${#PUB} -eq 44 ] || die "couldn't read the server key in $STATE/server.key"
	touch "$STATE/peers.tsv"

	# FwMark 0x80000 is the node agent's bypass mark: WireGuard's own packets
	# to the TVs then use the normal routes, not the exit node's.
	cat >"$STATE/interface.conf" <<EOF
[Interface]
Address = $SUBNET_PREFIX.1/24
ListenPort = $PORT
PrivateKey = $(cat "$STATE/server.key")
FwMark = 0x80000
MTU = 1280
PostUp = iptables -t nat -A POSTROUTING -s $SUBNET_PREFIX.0/24 ! -o %i -j MASQUERADE; iptables -A FORWARD -i %i -j ACCEPT; iptables -A FORWARD -o %i -m state --state RELATED,ESTABLISHED -j ACCEPT
PostDown = iptables -t nat -D POSTROUTING -s $SUBNET_PREFIX.0/24 ! -o %i -j MASQUERADE; iptables -D FORWARD -i %i -j ACCEPT; iptables -D FORWARD -o %i -m state --state RELATED,ESTABLISHED -j ACCEPT
EOF
	chmod 0600 "$STATE/interface.conf"

	DNS=1.1.1.1,9.9.9.9
	[ -f "$STATE/server.json" ] && DNS=$(jq -r '.dns | join(",")' "$STATE/server.json")
	jq -n --arg pub "$PUB" --arg ep "$HOST:$PORT" --arg dns "$DNS" --arg subnet "$SUBNET_PREFIX.0/24" \
		'{publicKey: $pub, endpoint: $ep, dns: ($dns | split(",")), mtu: 1280, subnet: $subnet}' >"$STATE/server.json"

	cat >/etc/sysctl.d/99-tappervpn-forwarding.conf <<'EOF'
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
EOF
	sysctl -q -p /etc/sysctl.d/99-tappervpn-forwarding.conf

	install -d -o headscale -g headscale -m 0750 "$DIR" "$DIR/pending" "$DIR/peers"
	sync_wg
	systemctl enable "wg-quick@$IF" >/dev/null 2>&1
	systemctl restart "wg-quick@$IF"

	# Settings for already paired devices pick up a new name or port.
	while IFS='	' read -r pub addr name; do
		[ -n "$pub" ] && write_peer_file "$pub" "$name" "$addr"
	done <"$STATE/peers.tsv"

	echo "WireGuard bridge $IF is up on UDP $PORT ($HOST:$PORT), key $PUB."
	echo "Forward UDP $PORT on the router to this server. Then pair a TV: wg-bridge approve <code>."
	ip link show "$MESH_IF" >/dev/null 2>&1 || echo "Note: $MESH_IF isn't up; TVs reach the internet but not your devices' VPN addresses (run join-exit-node.sh)."
}

cmd_list() {
	echo "Waiting for approval:"
	found=
	for f in "$DIR"/pending/*.json; do
		[ -f "$f" ] || continue
		found=1
		jq -r '"  \(.code)  \(.name)  (asked \(.created))"' "$f"
	done
	[ -n "$found" ] || echo "  none (codes expire after 15 minutes)"
	echo "Paired:"
	if [ -s "$STATE/peers.tsv" ]; then
		while IFS='	' read -r pub addr name; do
			hs=$(wg show "$IF" latest-handshakes 2>/dev/null | awk -v k="$pub" '$1 == k { print $2 }')
			when=never
			[ -n "$hs" ] && [ "$hs" != 0 ] && when=$(date -d "@$hs" '+%Y-%m-%d %H:%M')
			echo "  $addr  $name  (last seen $when)"
		done <"$STATE/peers.tsv"
	else
		echo "  none"
	fi
}

cmd_approve() {
	CODE=${1:?usage: wg-bridge approve <code> [name]}
	f=$DIR/pending/$CODE.json
	[ -f "$f" ] || die "no device is waiting with code $CODE (wg-bridge list)"
	[ -f "$STATE/server.json" ] || die "run wg-bridge install first"
	PUB=$(jq -r .publicKey "$f")
	NAME=${2:-$(jq -r .name "$f")}
	NAME=$(printf %s "$NAME" | tr -c 'A-Za-z0-9 ._-' '_')
	[ ${#PUB} -eq 44 ] || die "bad key in $f"
	if grep -q "^$PUB	" "$STATE/peers.tsv"; then
		ADDR=$(awk -F'\t' -v k="$PUB" '$1 == k { print $2 }' "$STATE/peers.tsv")
	else
		ADDR=$(next_addr)
		printf '%s\t%s\t%s\n' "$PUB" "$ADDR" "$NAME" >>"$STATE/peers.tsv"
	fi
	sync_wg
	write_peer_file "$PUB" "$NAME" "$ADDR"
	rm -f "$f"
	echo "Paired $NAME as $ADDR. The TV connects within a few seconds."
}

cmd_reject() {
	CODE=${1:?usage: wg-bridge reject <code>}
	rm -f "$DIR/pending/$CODE.json"
	echo "Rejected $CODE."
}

cmd_remove() {
	WHO=${1:?usage: wg-bridge remove <name or address>}
	line=$(awk -F'\t' -v w="$WHO" '$2 == w || $3 == w' "$STATE/peers.tsv" | head -1)
	[ -n "$line" ] || die "no paired device named or at $WHO (wg-bridge list)"
	PUB=$(printf %s "$line" | cut -f1)
	awk -F'\t' -v k="$PUB" '$1 != k' "$STATE/peers.tsv" >"$STATE/peers.tsv.tmp"
	mv -f "$STATE/peers.tsv.tmp" "$STATE/peers.tsv"
	rm -f "$DIR/peers/$(key_hex "$PUB").json"
	sync_wg
	echo "Removed $WHO. It has to be paired again to connect."
}

cmd_dns() {
	DNS=${1:?usage: wg-bridge dns <server>[,<server>...]}
	jq --arg dns "$DNS" '.dns = ($dns | split(","))' "$STATE/server.json" >"$STATE/server.json.tmp"
	mv -f "$STATE/server.json.tmp" "$STATE/server.json"
	while IFS='	' read -r pub addr name; do
		[ -n "$pub" ] && write_peer_file "$pub" "$name" "$addr"
	done <"$STATE/peers.tsv"
	echo "DNS for paired devices: $DNS (they pick it up the next time they connect)."
}

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
CMD=${1:-list}
[ $# -gt 0 ] && shift
case $CMD in
install) cmd_install "$@" ;;
list) cmd_list ;;
approve) cmd_approve "$@" ;;
reject) cmd_reject "$@" ;;
remove) cmd_remove "$@" ;;
dns) cmd_dns "$@" ;;
*) die "unknown command $CMD (install, list, approve, reject, remove, dns)" ;;
esac
