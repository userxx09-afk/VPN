#!/bin/sh
set -e
if [ -d /run/systemd/system ] ; then
	  systemctl --system daemon-reload >/dev/null || true
fi

if [ -x "/usr/bin/deb-systemd-helper" ]; then
    if [ "$1" = "remove" ]; then
		    deb-systemd-helper mask 'tappervpn.nginx-auth.socket' >/dev/null || true
		    deb-systemd-helper mask 'tappervpn.nginx-auth.service' >/dev/null || true
	  fi

    if [ "$1" = "purge" ]; then
		    deb-systemd-helper purge 'tappervpn.nginx-auth.socket' >/dev/null || true
		    deb-systemd-helper unmask 'tappervpn.nginx-auth.socket' >/dev/null || true
		    deb-systemd-helper purge 'tappervpn.nginx-auth.service' >/dev/null || true
		    deb-systemd-helper unmask 'tappervpn.nginx-auth.service' >/dev/null || true
	  fi
fi
