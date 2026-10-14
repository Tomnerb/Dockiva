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
                .frame(minWidth: 1080, minHeight: 700)
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in model.stop() }
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1300, height: 860)
        .commands {
            CommandGroup(replacing: .newItem) {}
            CommandGroup(after: .toolbar) {
                Button("Refresh") { Task { await model.refreshAll() } }.keyboardShortcut("r")
                Divider()
                ForEach(Array(SidebarItem.allCases.enumerated()), id: \.element) { index, item in
                    Button(item.title) { model.section = item }
                        .keyboardShortcut(KeyEquivalent(Character("\(index + 1)")), modifiers: .command)
                }
            }
        }

        MenuBarExtra {
            MenuBarPanel().environment(model)
        } label: {
            MenuBarLabel().environment(model)
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView().environment(model).frame(width: 460)
        }
    }
}

/// The menu bar icon. It exists from launch whatever windows macOS restores, so it
/// starts the core and opens the main window.
struct MenuBarLabel: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Image(systemName: model.runningCount > 0 ? "shippingbox.fill" : "shippingbox")
            .task {
                openWindow(id: "main")
                await model.start()
            }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    // The menu bar panel keeps containers in reach with the window closed.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
}

enum SidebarItem: String, CaseIterable, Identifiable {
    case overview, containers, images, volumes, networks, engine
    var id: String { rawValue }

    var title: String {
        switch self {
        case .overview: "Overview"
        case .containers: "Containers"
        case .images: "Images"
        case .volumes: "Volumes"
        case .networks: "Networks"
        case .engine: "Engine"
        }
    }

    var symbol: String {
        switch self {
        case .overview: "sparkles"
        case .containers: "shippingbox.fill"
        case .images: "square.stack.3d.up.fill"
        case .volumes: "externaldrive.fill"
        case .networks: "network"
        case .engine: "bolt.horizontal.circle.fill"
        }
    }

    var accent: Color {
        switch self {
        case .overview: DockivaPalette.cyan
        case .containers: DockivaPalette.teal
        case .images: DockivaPalette.blue
        case .volumes: .indigo
        case .networks: .mint
        case .engine: .orange
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var section: SidebarItem { model.section ?? .overview }

    var body: some View {
        ZStack {
            FeatureBackdrop(accent: section.accent)
            HStack(spacing: 0) {
                SidebarView().frame(width: 260)
                Group {
                    switch model.phase {
                    case .starting:
                        ProgressView("Starting Dockiva…").frame(maxWidth: .infinity, maxHeight: .infinity)
                    case .failed(let message):
                        EmptyStateCard(eyebrow: "SOMETHING WENT WRONG", title: "Dockiva could not start", detail: message,
                                       symbol: "exclamationmark.triangle.fill", accent: .orange)
                            .padding(40)
                            .frame(maxHeight: .infinity, alignment: .top)
                    case .ready:
                        screen
                    }
                }
                .id(section)
                .transition(reduceMotion ? .identity : .opacity.combined(with: .scale(scale: 0.992)))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .tint(section.accent)
            .environment(\.dockivaAccent, section.accent)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.22), value: section)
        }
        .preferredColorScheme(.dark)
        .alert("Action failed", isPresented: Binding(get: { model.lastError != nil }, set: { if !$0 { model.lastError = nil } })) {
            Button("OK") { model.lastError = nil }
        } message: {
            Text(model.lastError ?? "")
        }
    }

    @ViewBuilder private var screen: some View {
        switch section {
        case .overview: OverviewView()
        case .containers: ContainersView()
        case .images: ImagesView()
        case .volumes: VolumesView()
        case .networks: NetworksView()
        case .engine: EngineView()
        }
    }
}

struct SidebarView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Namespace private var selection
    @State private var hovered: SidebarItem?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            brand
            VStack(spacing: 8) {
                ForEach(SidebarItem.allCases.filter { $0 != .engine }) { row($0) }
            }
            .padding(.horizontal, 16)
            Spacer(minLength: 20)
            row(.engine, compact: true).padding(.horizontal, 16)
            engineStatus.padding(16)
        }
    }

    private var brand: some View {
        HStack(spacing: 12) {
            AppIconImage()
                .frame(width: 42, height: 42)
                .shadow(color: .cyan.opacity(0.25), radius: 10, y: 4)
            VStack(alignment: .leading, spacing: 1) {
                Text("Dockiva").font(.system(size: 18, weight: .bold, design: .rounded))
                Text("Dockiva Engine").font(.caption).foregroundStyle(.white.opacity(0.52))
            }
        }
        .padding(.horizontal, 22)
        .padding(.top, 34)
        .padding(.bottom, 26)
    }

    private func count(_ item: SidebarItem) -> Int? {
        switch item {
        case .containers: model.containers.count
        case .images: model.images.count
        case .volumes: model.volumes.count
        case .networks: model.networks.count
        default: nil
        }
    }

    private func row(_ item: SidebarItem, compact: Bool = false) -> some View {
        let selected = (model.section ?? .overview) == item
        let tint = item.accent
        return Button {
            withAnimation(reduceMotion ? nil : .spring(response: 0.34, dampingFraction: 0.82)) { model.section = item }
        } label: {
            ZStack {
                if selected {
                    RoundedRectangle(cornerRadius: 15, style: .continuous)
                        .fill(LinearGradient(colors: [tint.opacity(0.28), tint.opacity(0.11)], startPoint: .leading, endPoint: .trailing))
                        .overlay(RoundedRectangle(cornerRadius: 15, style: .continuous).stroke(tint.opacity(0.55), lineWidth: 1))
                        .matchedGeometryEffect(id: "active", in: selection, properties: reduceMotion ? [] : .frame)
                        .shadow(color: tint.opacity(0.16), radius: 10, y: 4)
                } else if hovered == item {
                    RoundedRectangle(cornerRadius: 15, style: .continuous).fill(.white.opacity(0.065))
                }
                rowLabel(item, selected: selected, compact: compact)
            }
            .frame(maxWidth: .infinity)
            .frame(height: compact ? 42 : 50)
            .contentShape(RoundedRectangle(cornerRadius: 15, style: .continuous))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(item.title)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .onHover { inside in
            withAnimation(reduceMotion ? nil : .easeOut(duration: 0.16)) { hovered = inside ? item : (hovered == item ? nil : hovered) }
        }
    }

    private func rowLabel(_ item: SidebarItem, selected: Bool, compact: Bool) -> some View {
        let tint = item.accent
        return HStack(spacing: 14) {
            ZStack {
                RoundedRectangle(cornerRadius: 9, style: .continuous).fill(tint.opacity(selected ? 0.24 : 0.11))
                Image(systemName: item.symbol)
                    .font(.system(size: compact ? 14 : 15, weight: .semibold))
                    .foregroundStyle(selected ? Color.white : tint)
            }
            .frame(width: compact ? 31 : 36, height: compact ? 31 : 36)
            Text(item.title).font(.system(size: compact ? 13 : 15, weight: selected ? .bold : .semibold, design: .rounded))
            Spacer()
            if let n = count(item), n > 0 {
                Text("\(n)").font(.caption.monospacedDigit()).foregroundStyle(.white.opacity(0.45))
            }
            if selected {
                Circle().fill(tint).frame(width: 6, height: 6).shadow(color: tint, radius: 5)
            }
        }
        .foregroundStyle(.white.opacity(selected ? 1 : 0.76))
        .padding(.horizontal, 12)
    }

    private var engineStatus: some View {
        let connected = model.status?.connected == true
        return HStack(spacing: 9) {
            Circle().fill(connected ? Color.green : Color.orange).frame(width: 8, height: 8).shadow(color: connected ? .green : .orange, radius: 5)
            VStack(alignment: .leading, spacing: 1) {
                Text(connected ? "Engine running" : "Engine stopped").font(.caption.weight(.semibold))
                Text(connected ? "\(model.runningCount) of \(model.containers.count) containers up" : "Start it from Overview")
                    .font(.caption2).foregroundStyle(.white.opacity(0.45))
            }
        }
        .foregroundStyle(.white.opacity(0.82))
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.black.opacity(0.18), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
    }
}

struct MenuBarPanel: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                AppIconImage().frame(width: 24, height: 24)
                Text("Dockiva").font(.headline)
                Spacer()
                StatusDot(running: model.status?.connected == true)
                Text(model.status?.connected == true ? "\(model.runningCount) running" : "Stopped")
                    .font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
            }
            Divider()
            monitorRow("CPU", value: String(format: "%.1f%%", model.totalCPU), progress: model.totalCPU / 100 / Double(ProcessInfo.processInfo.activeProcessorCount))
            monitorRow("Memory", value: model.totalMemory.byteString,
                       progress: Double(model.totalMemory) / Double(ProcessInfo.processInfo.physicalMemory))
            Divider()
            if model.containers.isEmpty {
                Text("No containers").font(.caption).foregroundStyle(.secondary)
            }
            ScrollView {
                VStack(spacing: 2) {
                    ForEach(model.containers.prefix(30)) { container in containerRow(container) }
                }
            }
            .frame(maxHeight: 260)
            Divider()
            Button {
                model.section = .overview
                openWindow(id: "main")
                NSApp.activate(ignoringOtherApps: true)
            } label: { Label("Open Dockiva", systemImage: "macwindow") }
                .buttonStyle(.plain)
            Button { NSApp.terminate(nil) } label: { Label("Quit Dockiva", systemImage: "power") }
                .buttonStyle(.plain)
        }
        .padding(16)
        .frame(width: 320)
    }

    private func monitorRow(_ title: String, value: String, progress: Double) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title).font(.caption.weight(.semibold))
                Spacer()
                Text(value).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            ProgressView(value: min(max(progress, 0), 1)).tint(DockivaPalette.cyan)
        }
    }

    private func containerRow(_ container: ContainerInfo) -> some View {
        HStack(spacing: 8) {
            StatusDot(running: container.isRunning)
            Text(container.name).font(.callout).lineLimit(1)
            Spacer()
            if model.busy.contains(container.id) {
                ProgressView().controlSize(.mini)
            } else {
                Button {
                    Task { await model.containerAction(container.isRunning ? "stop" : "start", container) }
                } label: {
                    Image(systemName: container.isRunning ? "stop.fill" : "play.fill").font(.caption)
                }
                .buttonStyle(.plain)
                .help(container.isRunning ? "Stop" : "Start")
            }
        }
        .padding(.vertical, 4)
        .padding(.horizontal, 6)
    }
}
