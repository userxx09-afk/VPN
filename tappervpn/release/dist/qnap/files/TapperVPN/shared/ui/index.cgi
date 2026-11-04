#!/bin/sh
CONF=/etc/config/qpkg.conf
QPKG_NAME="TapperVPN"
QPKG_ROOT=$(/sbin/getcfg ${QPKG_NAME} Install_Path -f ${CONF} -d"")
exec "${QPKG_ROOT}/tappervpn" --socket=/tmp/tappervpn/tappervpnd.sock web --cgi --prefix="/cgi-bin/qpkg/TapperVPN/index.cgi/"
