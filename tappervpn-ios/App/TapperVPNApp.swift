// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

import SwiftUI

@main
struct TapperVPNApp: App {
    @StateObject private var vpn = VPNController()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(vpn)
        }
    }
}

enum AppSection: String, CaseIterable, Identifiable {
    case home = "Home"
    case devices = "Devices"
    case exitNode = "Exit node"
    case settings = "Settings"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .home: return "shield.lefthalf.filled"
        case .devices: return "laptopcomputer.and.iphone"
        case .exitNode: return "arrow.triangle.branch"
        case .settings: return "gearshape"
        }
    }
}

/// Sign-in until this device has joined; then a sidebar (iPad) or a stack
/// (iPhone, iPad in Slide Over) with the app's sections.
struct RootView: View {
    @EnvironmentObject var vpn: VPNController
    @State private var selection: AppSection? = .home

    var body: some View {
        Group {
            if vpn.needsSignIn {
                SignInView()
            } else {
                NavigationSplitView {
                    List(AppSection.allCases, selection: $selection) { s in
                        Label(s.rawValue, systemImage: s.icon).tag(s)
                    }
                    .navigationTitle("TapperVPN")
                    .safeAreaInset(edge: .bottom) { SidebarStatus() }
                } detail: {
                    NavigationStack {
                        switch selection ?? .home {
                        case .home: HomeView()
                        case .devices: DevicesView()
                        case .exitNode: ExitNodeView()
                        case .settings: SettingsView()
                        }
                    }
                }
            }
        }
        .overlay(alignment: .top) { BusyBanner() }
        .alert("TapperVPN", isPresented: Binding(
            get: { vpn.errorMessage != nil },
            set: { if !$0 { vpn.errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(vpn.errorMessage ?? "")
        }
    }
}

struct SidebarStatus: View {
    @EnvironmentObject var vpn: VPNController

    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(vpn.statusColor).frame(width: 10, height: 10)
            Text(vpn.statusText).font(.footnote).foregroundStyle(.secondary)
            Spacer()
        }
        .padding()
    }
}

struct BusyBanner: View {
    @EnvironmentObject var vpn: VPNController

    var body: some View {
        if let busy = vpn.busy {
            HStack(spacing: 8) {
                ProgressView()
                Text(busy + "…")
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(.regularMaterial, in: Capsule())
            .padding(.top, 8)
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }
}

extension VPNController {
    var statusText: String {
        switch vpnStatus {
        case .connected:
            switch model?.state {
            case "Running": return "Connected"
            case "Starting": return "Starting"
            case "Stopped": return "Paused"
            case "NeedsLogin": return "Waiting for sign-in"
            case "NeedsMachineAuth": return "Waiting for approval"
            default: return "Connected"
            }
        case .connecting: return "Connecting"
        case .reasserting: return "Reconnecting"
        case .disconnecting: return "Disconnecting"
        default: return "Disconnected"
        }
    }

    var statusColor: Color {
        switch vpnStatus {
        case .connected: return model?.state == "Running" ? .green : .orange
        case .connecting, .reasserting, .disconnecting: return .orange
        default: return .gray
        }
    }
}
