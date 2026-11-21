// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

import SwiftUI
import UIKit

// MARK: - Sign in

struct SignInView: View {
    @EnvironmentObject var vpn: VPNController
    @AppStorage("server", store: Shared.defaults) private var server = Shared.defaultServer
    @State private var deviceName = Shared.defaults.string(forKey: "deviceName") ?? ""
    @State private var authKey = ""

    var body: some View {
        NavigationStack {
            Form {
                SwiftUI.Section {
                    HStack(spacing: 16) {
                        Image("Logo").resizable().frame(width: 64, height: 64)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        VStack(alignment: .leading, spacing: 4) {
                            Text("TapperVPN").font(.title2.bold())
                            Text("Join this iPad to your TapperVPN network.")
                                .foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, 8)
                }

                if let url = vpn.model?.authURL, !url.isEmpty {
                    SwiftUI.Section("Waiting for approval") {
                        Text("Approve this device on the server, or sign in on the page that opened. TapperVPN carries on by itself once it's approved.")
                        if let u = URL(string: url) {
                            Link("Open the approval page again", destination: u)
                        }
                        Button("Copy the approval link") { UIPasteboard.general.string = url }
                    }
                }

                SwiftUI.Section {
                    TextField("Server", text: $server)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Device name (for example, Kitchen iPad)", text: $deviceName)
                        .textInputAutocapitalization(.words)
                    SecureField("Auth key (optional)", text: $authKey)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: {
                    Text("Sign in")
                } footer: {
                    Text("With an auth key the iPad joins by itself. Without one, the server shows a page where the sign-in is approved. iOS asks once to allow TapperVPN to add a VPN configuration.")
                }

                SwiftUI.Section {
                    Button {
                        Task { await vpn.signIn(server: server, authKey: authKey, deviceName: deviceName) }
                    } label: {
                        HStack {
                            Spacer()
                            Text("Sign in").bold()
                            Spacer()
                        }
                    }
                    .disabled(vpn.busy != nil || server.trimmingCharacters(in: .whitespaces).isEmpty)
                    if vpn.isOn {
                        Button("Cancel and turn the VPN off", role: .destructive) { vpn.disconnect() }
                    }
                }
            }
            .frame(maxWidth: 640)
            .frame(maxWidth: .infinity)
            .navigationTitle("Sign in")
        }
    }
}

// MARK: - Home

struct HomeView: View {
    @EnvironmentObject var vpn: VPNController

    var body: some View {
        Form {
            SwiftUI.Section {
                VStack(spacing: 20) {
                    ZStack {
                        Circle()
                            .fill(vpn.statusColor.opacity(0.15))
                            .frame(width: 140, height: 140)
                        Image(systemName: vpn.isOn ? "lock.shield.fill" : "shield.slash")
                            .font(.system(size: 60))
                            .foregroundStyle(vpn.statusColor)
                    }
                    Text(vpn.statusText).font(.title.bold())
                    Toggle(isOn: Binding(
                        get: { vpn.isOn },
                        set: { on in
                            if on { Task { await vpn.connect() } } else { vpn.disconnect() }
                        }
                    )) {
                        Text(vpn.isOn ? "VPN is on" : "VPN is off").font(.headline)
                    }
                    .toggleStyle(.switch)
                    .frame(maxWidth: 320)
                    .disabled(vpn.busy != nil)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 16)
            }

            if let me = vpn.model?.me {
                SwiftUI.Section("This device") {
                    LabeledContent("Name", value: me.name)
                    CopyRow(title: "VPN address", value: me.ip)
                    if let v6 = me.ipv6 { CopyRow(title: "IPv6", value: v6) }
                    if !me.dnsName.isEmpty { CopyRow(title: "DNS name", value: me.dnsName) }
                }
            }

            if vpn.model != nil {
                SwiftUI.Section("Exit node") {
                    NavigationLink {
                        ExitNodeView()
                    } label: {
                        LabeledContent("Traffic goes out through", value: vpn.model?.exit?.name ?? "Direct (none)")
                    }
                    if let exit = vpn.model?.exit, !exit.online {
                        Label("\(exit.name) is offline", systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.orange)
                    }
                }
            } else if vpn.signedIn {
                SwiftUI.Section {
                    Text("Turn the VPN on to see your devices and choose an exit node.")
                        .foregroundStyle(.secondary)
                }
            }

            if let health = vpn.model?.health, !health.isEmpty {
                SwiftUI.Section("Warnings") {
                    ForEach(health, id: \.self) { Text($0).font(.footnote) }
                }
            }

            SwiftUI.Section {
                Link(destination: Shared.adminURL) {
                    Label("Open the admin page", systemImage: "safari")
                }
            }
        }
        .navigationTitle("Home")
    }
}

// MARK: - Devices

struct DevicesView: View {
    @EnvironmentObject var vpn: VPNController
    @State private var search = ""

    private var devices: [Device] {
        let all = vpn.model?.devices ?? []
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return all }
        return all.filter { $0.name.lowercased().contains(q) || $0.ip.contains(q) || $0.os.lowercased().contains(q) }
    }

    var body: some View {
        Group {
            if vpn.model == nil {
                ContentUnavailable(title: "VPN is off", systemImage: "laptopcomputer.and.iphone",
                                   message: "Turn the VPN on to see your devices.")
            } else {
                List(devices) { d in
                    DeviceRow(device: d)
                }
                .searchable(text: $search, prompt: "Name, address or system")
            }
        }
        .navigationTitle("Devices")
    }
}

struct DeviceRow: View {
    let device: Device
    @State private var copied = false

    private var subtitle: String {
        var parts: [String] = []
        if !device.os.isEmpty { parts.append(device.os) }
        if !device.online, let seen = device.lastSeen { parts.append("seen " + seen) }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            Circle()
                .fill(device.online ? Color.green : Color.gray.opacity(0.5))
                .frame(width: 10, height: 10)
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(device.name).font(.body.weight(.medium))
                    if device.exitNode {
                        Text("exit node")
                            .font(.caption2)
                            .padding(.horizontal, 6).padding(.vertical, 2)
                            .background(Color.accentColor.opacity(0.15), in: Capsule())
                    }
                }
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                UIPasteboard.general.string = device.ip
                copied = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
            } label: {
                HStack(spacing: 6) {
                    Text(device.ip).font(.callout.monospacedDigit())
                    Image(systemName: copied ? "checkmark" : "doc.on.doc")
                }
            }
            .buttonStyle(.borderless)
        }
        .contextMenu {
            Button("Copy VPN address") { UIPasteboard.general.string = device.ip }
            if let v6 = device.ipv6 { Button("Copy IPv6 address") { UIPasteboard.general.string = v6 } }
            if !device.dnsName.isEmpty { Button("Copy DNS name") { UIPasteboard.general.string = device.dnsName } }
        }
    }
}

// MARK: - Exit node

struct ExitNodeView: View {
    @EnvironmentObject var vpn: VPNController

    var body: some View {
        Group {
            if let model = vpn.model {
                let exits = model.devices.filter(\.exitNode)
                Form {
                    SwiftUI.Section {
                        ExitChoice(name: "None", detail: "Only TapperVPN devices go through the VPN",
                                   selected: model.exit == nil, online: true) {
                            Task { await vpn.setExitNode(nil) }
                        }
                        ForEach(exits) { d in
                            ExitChoice(name: d.name, detail: d.online ? d.os : "offline",
                                       selected: model.exit?.id == d.id, online: d.online) {
                                Task { await vpn.setExitNode(d.id) }
                            }
                        }
                    } header: {
                        Text("Send all traffic through")
                    } footer: {
                        if exits.isEmpty {
                            Text("No device offers itself as an exit node yet.")
                        }
                    }

                    SwiftUI.Section {
                        Toggle("Allow local network access", isOn: Binding(
                            get: { model.allowLAN },
                            set: { on in Task { await vpn.setAllowLAN(on) } }
                        ))
                    } footer: {
                        Text("While an exit node is in use, keep reaching printers and other devices on this Wi-Fi directly.")
                    }
                }
            } else {
                ContentUnavailable(title: "VPN is off", systemImage: "arrow.triangle.branch",
                                   message: "Turn the VPN on to choose an exit node.")
            }
        }
        .navigationTitle("Exit node")
    }
}

struct ExitChoice: View {
    let name: String
    let detail: String
    let selected: Bool
    let online: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(name).foregroundStyle(online ? .primary : .secondary)
                    Text(detail).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if selected { Image(systemName: "checkmark").foregroundStyle(Color.accentColor) }
            }
        }
        .tint(.primary)
    }
}

// MARK: - Settings

struct SettingsView: View {
    @EnvironmentObject var vpn: VPNController
    @State private var confirmSignOut = false

    private var version: String {
        let v = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let b = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "?"
        return "\(v) (\(b))"
    }

    var body: some View {
        Form {
            SwiftUI.Section("Account") {
                LabeledContent("Server", value: vpn.model?.controlURL ?? (Shared.defaults.string(forKey: "server") ?? Shared.defaultServer))
                LabeledContent("Device name", value: Shared.deviceName)
                Button("Sign out", role: .destructive) { confirmSignOut = true }
                    .disabled(vpn.model == nil)
            }
            SwiftUI.Section("Help") {
                NavigationLink("Logs") { LogView() }
                Link("Open the admin page", destination: Shared.adminURL)
            }
            SwiftUI.Section("About") {
                LabeledContent("Version", value: version)
                Text("TapperVPN is built on Tailscale (BSD-3-Clause). Updates come through TestFlight.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
        }
        .navigationTitle("Settings")
        .confirmationDialog("Sign this iPad out of TapperVPN?", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) { Task { await vpn.signOut() } }
        }
    }
}

struct LogView: View {
    @State private var text = ""

    var body: some View {
        ScrollView {
            Text(text.isEmpty ? "No log yet." : text)
                .font(.caption.monospaced())
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding()
        }
        .navigationTitle("Logs")
        .toolbar {
            ToolbarItemGroup {
                Button { load() } label: { Image(systemName: "arrow.clockwise") }
                ShareLink(item: text) { Image(systemName: "square.and.arrow.up") }
            }
        }
        .onAppear(perform: load)
    }

    /// Shows the last 64 KB of the tunnel's log.
    private func load() {
        guard let h = try? FileHandle(forReadingFrom: Shared.logFile) else { text = ""; return }
        defer { try? h.close() }
        let size = (try? h.seekToEnd()) ?? 0
        let start = size > 65_536 ? size - 65_536 : 0
        try? h.seek(toOffset: start)
        text = String(decoding: h.readDataToEndOfFile(), as: UTF8.self)
    }
}

// MARK: - Shared pieces

struct CopyRow: View {
    let title: String
    let value: String
    @State private var copied = false

    var body: some View {
        Button {
            UIPasteboard.general.string = value
            copied = true
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
        } label: {
            HStack {
                Text(title).foregroundStyle(.primary)
                Spacer()
                Text(value).foregroundStyle(.secondary).font(.body.monospacedDigit())
                Image(systemName: copied ? "checkmark" : "doc.on.doc").foregroundStyle(Color.accentColor)
            }
        }
    }
}

/// ContentUnavailableView needs iOS 17; this works on 16.
struct ContentUnavailable: View {
    let title: String
    let systemImage: String
    let message: String

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: systemImage).font(.system(size: 48)).foregroundStyle(.secondary)
            Text(title).font(.title2.bold())
            Text(message).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .padding()
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
