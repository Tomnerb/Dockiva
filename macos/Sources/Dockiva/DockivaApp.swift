import AppKit
import SwiftUI

@main
struct DockivaApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var model = AppModel()

    var body: some Scene {
        Window("Dockiva", id: "main") {
            RootView()
                .environment(model)
                .frame(minWidth: 960, minHeight: 560)
                .task { await model.start() }
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in model.stop() }
        }
        .defaultSize(width: 1320, height: 820)
        .commands {
            CommandGroup(after: .toolbar) {
                Button("Refresh") { Task { await model.refreshAll() } }.keyboardShortcut("r")
                Divider()
                ForEach(Array(SidebarItem.allCases.enumerated()), id: \.element) { index, item in
                    Button(item.rawValue) { model.section = item }
                        .keyboardShortcut(KeyEquivalent(Character("\(index + 1)")), modifiers: .command)
                }
            }
        }

        MenuBarExtra {
            MenuBarContent().environment(model)
        } label: {
            Image(systemName: model.runningCount > 0 ? "shippingbox.fill" : "shippingbox")
        }

        Settings {
            SettingsView().environment(model).frame(width: 460)
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    // The menu bar item keeps containers in reach with the window closed.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
}

enum SidebarItem: String, CaseIterable, Identifiable {
    case containers = "Containers", images = "Images", volumes = "Volumes", networks = "Networks", engine = "Engine"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .containers: "shippingbox"
        case .images: "square.stack.3d.up"
        case .volumes: "externaldrive"
        case .networks: "network"
        case .engine: "engine.combustion"
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        NavigationSplitView {
            List(SidebarItem.allCases, selection: $model.section) { item in
                Label(item.rawValue, systemImage: item.symbol).badge(badge(item))
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190)
            .safeAreaInset(edge: .bottom) { EngineBadge().padding(10) }
        } detail: {
            switch model.phase {
            case .starting:
                ProgressView("Starting Dockiva…")
            case .failed(let message):
                ContentUnavailableView("Dockiva could not start", systemImage: "exclamationmark.triangle", description: Text(message))
            case .ready:
                switch model.section ?? .containers {
                case .containers: ContainersView()
                case .images: ImagesView()
                case .volumes: VolumesView()
                case .networks: NetworksView()
                case .engine: EngineView()
                }
            }
        }
        .alert("Action failed", isPresented: Binding(get: { model.lastError != nil }, set: { if !$0 { model.lastError = nil } })) {
            Button("OK") { model.lastError = nil }
        } message: {
            Text(model.lastError ?? "")
        }
    }

    private func badge(_ section: SidebarItem) -> Int {
        switch section {
        case .containers: model.containers.count
        case .images: model.images.count
        case .volumes: model.volumes.count
        case .networks: model.networks.count
        case .engine: 0
        }
    }
}

struct EngineBadge: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(model.status?.connected == true ? Color.green : Color.secondary).frame(width: 8, height: 8)
            Text(model.status?.connected == true ? "Engine running" : "Engine stopped")
                .font(.caption).foregroundStyle(.secondary)
            Spacer()
        }
    }
}

struct MenuBarContent: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(model.status?.connected == true ? "\(model.runningCount) of \(model.containers.count) containers running" : "Engine stopped")
        Divider()
        ForEach(model.containers.prefix(15)) { container in
            Menu {
                if container.isRunning {
                    Button("Stop") { Task { await model.containerAction("stop", container) } }
                    Button("Restart") { Task { await model.containerAction("restart", container) } }
                } else {
                    Button("Start") { Task { await model.containerAction("start", container) } }
                }
                ForEach(container.ports ?? [], id: \.self) { port in
                    if let url = port.url.flatMap(URL.init(string:)) {
                        Button("Open \(port.display)") { NSWorkspace.shared.open(url) }
                    }
                }
            } label: {
                Label(container.name, systemImage: container.isRunning ? "circle.fill" : "circle")
            }
        }
        Divider()
        Button("Open Dockiva") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        SettingsLink { Text("Settings…") }
        Button("Quit Dockiva") { NSApp.terminate(nil) }.keyboardShortcut("q")
    }
}
