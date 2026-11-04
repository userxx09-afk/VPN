#!/bin/sh
set -e
if [ -d /run/systemd/system ] ; then
	systemctl --system daemon-reload >/dev/null || true
fi

if [ -x "/usr/bin/deb-systemd-helper" ]; then
    if [ "$1" = "remove" ]; then
		deb-systemd-helper mask 'tappervpnd.service' >/dev/null || true
	fi

    if [ "$1" = "purge" ]; then
		deb-systemd-helper purge 'tappervpnd.service' >/dev/null || true
		deb-systemd-helper unmask 'tappervpnd.service' >/dev/null || true
		rm -rf /var/lib/tappervpn
	fi
fi
