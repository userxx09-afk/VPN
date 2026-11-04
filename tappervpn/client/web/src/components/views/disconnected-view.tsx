// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

import React from "react"
import TapperVPNIcon from "src/assets/icons/tappervpn-icon.svg?react"

/**
 * DisconnectedView is rendered after node logout.
 */
export default function DisconnectedView() {
  return (
    <>
      <TapperVPNIcon className="mx-auto" />
      <p className="mt-12 text-center text-text-muted">
        You logged out of this device. To reconnect it you will have to
        re-authenticate the device from either the TapperVPN app or the
        TapperVPN command line interface.
      </p>
    </>
  )
}
