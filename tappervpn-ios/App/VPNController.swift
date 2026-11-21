// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

import Foundation
import NetworkExtension
import UIKit

/// Mirrors Model in libtappervpn/api.go.
struct Model: Decodable, Equatable {
    var state: String
    var loggedIn: Bool
    var authURL: String?
    var controlURL: String
    var defaultURL: String
    var me: Device? // this device
    var exit: Device?
    var allowLAN: Bool
    var devices: [Device]
    var health: [String]?

    enum CodingKeys: String, CodingKey {
        case state, loggedIn, authURL, controlURL, defaultURL, exit, allowLAN, devices, health
        case me = "self"
    }
}

struct Device: Decodable, Equatable, Identifiable, Hashable {
    var id: String
    var name: String
    var dnsName: String
    var ip: String
    var ipv6: String?
    var os: String
    var online: Bool
    var exitNode: Bool
    var lastSeen: String?
}

private struct Reply: Decodable {
    var error: String?
    var model: Model?
}

/// Drives the packet tunnel: turns the VPN on and off through iOS, and sends
/// the app's requests to the Go client running inside it.
@MainActor
final class VPNController: ObservableObject {
    /// The tunnel as iOS sees it.
    @Published private(set) var vpnStatus: NEVPNStatus = .invalid
    /// The Go client's state; nil while the tunnel isn't running.
    @Published private(set) var model: Model?
    @Published var errorMessage: String?
    @Published private(set) var busy: String?
    /// Set once this device has signed in, so the app opens on the main
    /// screen (not sign-in) while the VPN is off.
    @Published private(set) var signedIn: Bool = Shared.defaults.bool(forKey: "signedIn")

    private var manager: NETunnelProviderManager?
    private var pollTask: Task<Void, Never>?
    private var openedAuthURL: String?

    static var extensionBundleID: String {
        (Bundle.main.bundleIdentifier ?? "com.tappervpn.ipn") + ".network-extension"
    }

    init() {
        NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: .main) { [weak self] note in
            guard let conn = note.object as? NEVPNConnection else { return }
            Task { @MainActor in self?.statusChanged(conn.status) }
        }
        Task { await load() }
    }

    var isOn: Bool { vpnStatus == .connected || vpnStatus == .connecting || vpnStatus == .reasserting }

    var needsSignIn: Bool {
        if let model { return !model.loggedIn || model.state == "NeedsLogin" }
        return !signedIn
    }

    // MARK: - Tunnel

    private func load() async {
        do {
            let managers = try await NETunnelProviderManager.loadAllFromPreferences()
            manager = managers.first
            if let manager { statusChanged(manager.connection.status) }
        } catch {
            errorMessage = "Couldn't read the VPN configuration: \(error.localizedDescription)"
        }
    }

    /// Returns the app's VPN configuration, creating it (iOS asks the user
    /// to allow it the first time) if needed.
    private func ensureManager() async throws -> NETunnelProviderManager {
        let m = manager ?? NETunnelProviderManager()
        let proto = (m.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.extensionBundleID
        proto.serverAddress = "TapperVPN"
        m.protocolConfiguration = proto
        m.localizedDescription = "TapperVPN"
        m.isEnabled = true
        try await m.saveToPreferences()
        try await m.loadFromPreferences() // required before the first start
        manager = m
        return m
    }

    func connect() async {
        do {
            busy = "Connecting"
            defer { busy = nil }
            let m = try await ensureManager()
            if !isOn {
                try m.connection.startVPNTunnel()
            }
            try await waitUntilConnected()
            // The tunnel resumes the Go client; make sure it's not paused.
            if model?.loggedIn == true { _ = try? await send(["op": "setRunning", "on": true]) }
        } catch {
            errorMessage = "Couldn't connect: \(error.localizedDescription)"
        }
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
    }

    /// Waits for iOS to bring the tunnel up and the Go client to answer.
    private func waitUntilConnected() async throws {
        var sawStart = false
        for _ in 0..<60 { // 30 s
            let status = manager?.connection.status ?? .invalid
            if status == .connected {
                vpnStatus = .connected
                for _ in 0..<20 {
                    if (try? await send(["op": "status"])) != nil { return }
                    try await Task.sleep(nanoseconds: 250_000_000)
                }
                return
            }
            if status == .connecting || status == .reasserting { sawStart = true }
            if sawStart && (status == .disconnected || status == .invalid) { break }
            try await Task.sleep(nanoseconds: 500_000_000)
        }
        throw TunnelError("the VPN didn't start. Check Settings > VPN, then try again.")
    }

    private func statusChanged(_ status: NEVPNStatus) {
        vpnStatus = status
        if status == .connected {
            startPolling()
        } else {
            pollTask?.cancel()
            pollTask = nil
            if status == .disconnected || status == .invalid { model = nil }
        }
    }

    private func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                _ = try? await self?.send(["op": "status"])
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    // MARK: - Requests to the Go client

    @discardableResult
    private func send(_ request: [String: Any]) async throws -> Model {
        guard let session = manager?.connection as? NETunnelProviderSession, vpnStatus == .connected else {
            throw TunnelError("TapperVPN isn't connected")
        }
        let data = try JSONSerialization.data(withJSONObject: request)
        let reply: Data = try await withCheckedThrowingContinuation { cont in
            do {
                try session.sendProviderMessage(data) { response in
                    if let response {
                        cont.resume(returning: response)
                    } else {
                        cont.resume(throwing: TunnelError("TapperVPN didn't answer"))
                    }
                }
            } catch {
                cont.resume(throwing: error)
            }
        }
        let r = try JSONDecoder().decode(Reply.self, from: reply)
        if let err = r.error { throw TunnelError(err) }
        guard let m = r.model else { throw TunnelError("empty reply") }
        update(m)
        return m
    }

    private func update(_ m: Model) {
        if model != m { model = m }
        let nowSignedIn = m.loggedIn && m.state != "NeedsLogin"
        if nowSignedIn != signedIn {
            signedIn = nowSignedIn
            Shared.defaults.set(nowSignedIn, forKey: "signedIn")
        }
        // Without an auth key, the server gives a page where the sign-in is
        // approved: open it once.
        if let url = m.authURL, !url.isEmpty, url != openedAuthURL, let u = URL(string: url) {
            openedAuthURL = url
            UIApplication.shared.open(u)
        }
    }

    /// Runs one action and shows its error, if any.
    private func perform(_ what: String, _ request: [String: Any]) async {
        busy = what
        defer { busy = nil }
        do {
            try await send(request)
        } catch {
            errorMessage = "\(what) failed: \(error.localizedDescription)"
        }
    }

    // MARK: - Actions

    func signIn(server: String, authKey: String, deviceName: String) async {
        let name = deviceName.trimmingCharacters(in: .whitespacesAndNewlines)
        Shared.deviceName = name.isEmpty ? "iPad" : name
        openedAuthURL = nil
        await connect()
        guard vpnStatus == .connected else { return }
        await perform("Signing in", ["op": "signIn", "server": server, "authKey": authKey])
    }

    func signOut() async {
        await perform("Signing out", ["op": "signOut"])
        signedIn = false
        Shared.defaults.set(false, forKey: "signedIn")
        disconnect()
    }

    func setExitNode(_ id: String?) async {
        await perform("Changing the exit node", ["op": "setExitNode", "id": id ?? ""])
    }

    func setAllowLAN(_ on: Bool) async {
        await perform("Changing LAN access", ["op": "setAllowLAN", "on": on])
    }
}

struct TunnelError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
