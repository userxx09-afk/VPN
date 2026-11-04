#!/usr/bin/env bash

set -e

VERSION=0.1.3
for ARCH in amd64 arm64; do
    CGO_ENABLED=0 GOARCH=${ARCH} GOOS=linux go build -o tappervpn.nginx-auth .

    mkpkg \
        --out=tappervpn-nginx-auth-${VERSION}-${ARCH}.deb \
        --name=tappervpn-nginx-auth \
        --version=${VERSION} \
        --type=deb \
        --arch=${ARCH} \
        --postinst=deb/postinst.sh \
        --postrm=deb/postrm.sh \
        --prerm=deb/prerm.sh \
        --description="TapperVPN NGINX authentication protocol handler" \
        --files=./tappervpn.nginx-auth:/usr/sbin/tappervpn.nginx-auth,./tappervpn.nginx-auth.socket:/lib/systemd/system/tappervpn.nginx-auth.socket,./tappervpn.nginx-auth.service:/lib/systemd/system/tappervpn.nginx-auth.service,./README.md:/usr/share/tappervpn/nginx-auth/README.md

    mkpkg \
        --out=tappervpn-nginx-auth-${VERSION}-${ARCH}.rpm \
        --name=tappervpn-nginx-auth \
        --version=${VERSION} \
        --type=rpm \
        --arch=${ARCH} \
        --postinst=rpm/postinst.sh \
        --postrm=rpm/postrm.sh \
        --prerm=rpm/prerm.sh \
        --description="TapperVPN NGINX authentication protocol handler" \
        --files=./tappervpn.nginx-auth:/usr/sbin/tappervpn.nginx-auth,./tappervpn.nginx-auth.socket:/lib/systemd/system/tappervpn.nginx-auth.socket,./tappervpn.nginx-auth.service:/lib/systemd/system/tappervpn.nginx-auth.service,./README.md:/usr/share/tappervpn/nginx-auth/README.md
done
