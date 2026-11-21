// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

import Libtappervpn
import NetworkExtension
import os

/// The packet tunnel: runs the Go TapperVPN client (libtappervpn) while the
/// VPN is on. The app talks to it with sendProviderMessage; each message is
/// a JSON request handed to LibtappervpnCall (see libtappervpn/api.go).
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let log = Logger(subsystem: "com.tappervpn.ipn", category: "tunnel")
    private let calls = DispatchQueue(label: "com.tappervpn.calls", attributes: .concurrent)
    private var host: TunnelHost?

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        log.info("startTunnel")
        // The utun interface exists once settings have been applied, and
        // Go looks for it at Start, so apply placeholder settings first.
        // Go replaces them as soon as it knows the device's addresses.
        setTunnelNetworkSettings(TunnelHost.placeholderSettings()) { [weak self] error in
            guard let self else { return }
            if let error {
                self.log.error("placeholder settings: \(error.localizedDescription, privacy: .public)")
                completionHandler(error)
                return
            }
            let host = TunnelHost(provider: self)
            self.host = host
            DispatchQueue.global(qos: .userInitiated).async {
                var err: NSError?
                LibtappervpnStart(Shared.dataDir.path, host, &err)
                if let err {
                    self.log.error("start: \(err.localizedDescription, privacy: .public)")
                    completionHandler(err)
                    return
                }
                completionHandler(nil)
            }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        log.info("stopTunnel, reason \(reason.rawValue)")
        LibtappervpnStop()
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        let request = String(decoding: messageData, as: UTF8.self)
        calls.async {
            let reply = LibtappervpnCall(request)
            completionHandler?(Data(reply.utf8))
        }
    }
}

/// What Go calls back into: network settings and device details.
final class TunnelHost: NSObject, LibtappervpnHostProtocol {
    private weak var provider: NEPacketTunnelProvider?
    private let systemVersionValue: String
    private let modelNameValue: String

    init(provider: NEPacketTunnelProvider) {
        self.provider = provider
        let v = ProcessInfo.processInfo.operatingSystemVersion
        systemVersionValue = "\(v.majorVersion).\(v.minorVersion).\(v.patchVersion)"
        var info = utsname()
        uname(&info)
        modelNameValue = withUnsafeBytes(of: &info.machine) { raw in
            String(decoding: raw.prefix(while: { $0 != 0 }), as: UTF8.self)
        }
    }

    func deviceName() -> String { Shared.deviceName }
    func systemVersion() -> String { systemVersionValue }
    func modelName() -> String { modelNameValue }

    func applyNetworkSettings(_ settingsJSON: String?) throws {
        guard let provider else { throw TunnelError("the tunnel has stopped") }
        let data = Data((settingsJSON ?? "").utf8)
        let cfg = try JSONDecoder().decode(NetworkSettings.self, from: data)
        let settings = cfg.ipv4Addresses.isEmpty && cfg.ipv6Addresses.isEmpty
            ? Self.placeholderSettings() // signed out: keep the interface, route nothing
            : cfg.packetTunnelSettings()

        let done = DispatchSemaphore(value: 0)
        var applyError: Error?
        provider.setTunnelNetworkSettings(settings) { error in
            applyError = error
            done.signal()
        }
        done.wait()
        if let applyError { throw applyError }
    }

    static func placeholderSettings() -> NEPacketTunnelNetworkSettings {
        let s = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "100.100.100.100")
        let v4 = NEIPv4Settings(addresses: ["100.100.100.101"], subnetMasks: ["255.255.255.255"])
        v4.includedRoutes = []
        s.ipv4Settings = v4
        s.mtu = 1280
        return s
    }
}

struct TunnelError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}

/// Mirrors NetworkSettings in libtappervpn/settings.go.
struct NetworkSettings: Decodable {
    struct IPv4: Decodable { let address: String; let mask: String }
    struct IPv6: Decodable { let address: String; let prefixLength: Int }
    struct DNS: Decodable {
        let servers: [String]
        let searchDomains: [String]
        let matchDomains: [String]
    }

    let mtu: Int
    let ipv4Addresses: [IPv4]
    let ipv6Addresses: [IPv6]
    let ipv4Routes: [IPv4]
    let ipv4ExcludeRoutes: [IPv4]
    let ipv6Routes: [IPv6]
    let ipv6ExcludeRoutes: [IPv6]
    let dns: DNS?

    private static func route(_ r: IPv4) -> NEIPv4Route {
        r.mask == "0.0.0.0" ? .default() : NEIPv4Route(destinationAddress: r.address, subnetMask: r.mask)
    }

    private static func route(_ r: IPv6) -> NEIPv6Route {
        r.prefixLength == 0 ? .default() : NEIPv6Route(destinationAddress: r.address, networkPrefixLength: NSNumber(value: r.prefixLength))
    }

    func packetTunnelSettings() -> NEPacketTunnelNetworkSettings {
        let s = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "100.100.100.100")
        s.mtu = NSNumber(value: mtu)
        if !ipv4Addresses.isEmpty {
            let v4 = NEIPv4Settings(addresses: ipv4Addresses.map(\.address), subnetMasks: ipv4Addresses.map(\.mask))
            v4.includedRoutes = ipv4Routes.map(Self.route)
            v4.excludedRoutes = ipv4ExcludeRoutes.map(Self.route)
            s.ipv4Settings = v4
        }
        if !ipv6Addresses.isEmpty {
            let v6 = NEIPv6Settings(addresses: ipv6Addresses.map(\.address),
                                    networkPrefixLengths: ipv6Addresses.map { NSNumber(value: $0.prefixLength) })
            v6.includedRoutes = ipv6Routes.map(Self.route)
            v6.excludedRoutes = ipv6ExcludeRoutes.map(Self.route)
            s.ipv6Settings = v6
        }
        if let dns, !dns.servers.isEmpty {
            let d = NEDNSSettings(servers: dns.servers)
            d.searchDomains = dns.searchDomains
            // Empty: these servers answer every name (exit node in use).
            d.matchDomains = dns.matchDomains.isEmpty ? [""] : dns.matchDomains
            s.dnsSettings = d
        }
        return s
    }
}
