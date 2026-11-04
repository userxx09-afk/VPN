// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

import Foundation

struct Notifications {
    // Stops the virtual machine and saves its state
    static var stop = Notification.Name("io.tappervpn.macvmhost.stop")

    // Pauses the virtual machine and exits without saving its state
    static var halt = Notification.Name("io.tappervpn.macvmhost.halt")
}
