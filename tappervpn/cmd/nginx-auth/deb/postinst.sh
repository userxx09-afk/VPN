if [ "$1" = "configure" ] || [ "$1" = "abort-upgrade" ] || [ "$1" = "abort-deconfigure" ] || [ "$1" = "abort-remove" ] ; then
	  deb-systemd-helper unmask 'tappervpn.nginx-auth.socket' >/dev/null || true
	  if deb-systemd-helper --quiet was-enabled 'tappervpn.nginx-auth.socket'; then
		    deb-systemd-helper enable 'tappervpn.nginx-auth.socket' >/dev/null || true
	  else
		    deb-systemd-helper update-state 'tappervpn.nginx-auth.socket' >/dev/null || true
	  fi

    if systemctl is-active tappervpn.nginx-auth.socket >/dev/null; then
        systemctl --system daemon-reload >/dev/null || true
        deb-systemd-invoke stop 'tappervpn.nginx-auth.service' >/dev/null || true
        deb-systemd-invoke restart 'tappervpn.nginx-auth.socket' >/dev/null || true
    fi
fi
