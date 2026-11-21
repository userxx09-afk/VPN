// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

import Foundation

/// Settings shared by the app and the packet tunnel extension through their
/// App Group.
enum Shared {
    /// The App Group, from the TapperAppGroup key that project.yml puts in
    /// both Info.plists.
    static let appGroup: String =
        Bundle.main.object(forInfoDictionaryKey: "TapperAppGroup") as? String ?? "group.com.tappervpn.ipn"

    static var defaults: UserDefaults { UserDefaults(suiteName: appGroup) ?? .standard }

    /// Where the Go side keeps its state and log.
    static var dataDir: URL {
        let base = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
            ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("tappervpn", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    static var logFile: URL { dataDir.appendingPathComponent("tappervpn.log") }

    /// The name this device registers with on the server (iOS no longer
    /// tells apps the name the user gave the device).
    static var deviceName: String {
        get { defaults.string(forKey: "deviceName") ?? "iPad" }
        set { defaults.set(newValue, forKey: "deviceName") }
    }

    static let defaultServer = "https://vp.tapper.co"
    /// The VPN admin page, as in the Windows app.
    static let adminURL = URL(string: "http://100.64.0.2:8096")!
}
