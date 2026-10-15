import AppKit
import SwiftUI

/// The scopes of DockerService.PruneDocker, with what each removes.
enum StoragePruneScope: String, CaseIterable, Identifiable {
    case system, containers, images, networks, volumes
    var id: String { rawValue }

    var title: String {
        switch self {
        case .system: "Everything unused"
        case .containers: "Stopped containers"
        case .images: "Dangling images"
        case .networks: "Unused networks"
        case .volumes: "Anonymous volumes"
        }
    }

    var symbol: String {
        switch self {
        case .system: "sparkles"
        case .containers: "shippingbox"
        case .images: "square.stack.3d.up"
        case .networks: "network"
        case .volumes: "externaldrive"
        }
    }

    var removes: String {
        switch self {
        case .system: "Stopped containers, unused networks, dangling images and build cache. Volumes and tagged images are kept."
        case .containers: "Every stopped container."
        case .images: "Untagged (dangling) images. Tagged images are kept."
        case .networks: "Networks no container is attached to."
        case .volumes: "Unused anonymous volumes. Named volumes are kept."
        }
    }
}

/// Disk used by the engine, prune actions, and engines' data folders on this Mac.
@MainActor
struct StorageView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dockivaAccent) private var accent
    @State private var usage: [StorageDiskUsageItem] = []
    @State private var usageError: String?
    @State private var folders: [StorageHostItem] = []
    @State private var busy: String?  // "load", a prune scope or a folder ID
    @State private var pruning: StoragePruneScope?
    @State private var cleaning: StorageHostItem?
    @State private var output: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                FeatureHeader(title: "Storage", subtitle: "What the engine keeps on disk, and what you can safely remove",
                              symbol: "internaldrive.fill", accent: accent) {
                    HStack(spacing: 8) {
                        if busy != nil { ProgressView().controlSize(.small) }
                        Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                            .buttonStyle(SecondaryButtonStyle()).help("Refresh").disabled(busy != nil)
                    }
                }
                usageCards
                pruneCard
                if !folders.isEmpty { foldersCard }
            }
            .padding(28)
        }
        .task { await load() }
        .confirmationDialog(pruning.map { "Remove \($0.title.lowercased())?" } ?? "", isPresented: present($pruning),
                            titleVisibility: .visible, presenting: pruning) { scope in
            Button("Remove", role: .destructive) { Task { await prune(scope) } }
            Button("Cancel", role: .cancel) {}
        } message: { scope in
            Text(pruneMessage(scope))
        }
        .confirmationDialog(cleaning.map { "Clean \($0.name)?" } ?? "", isPresented: present($cleaning),
                            titleVisibility: .visible, presenting: cleaning) { item in
            Button("Build cache only") { Task { await clean(item, mode: "cache") } }
            Button("Unused resources") { Task { await clean(item, mode: "safe") } }
            Button("Deep cleanup", role: .destructive) { Task { await clean(item, mode: "deep") } }
            Button("Cancel", role: .cancel) {}
        } message: { item in
            Text(cleanMessage(item))
        }
        .sheet(item: Binding(get: { output.map(OutputSheet.init) }, set: { if $0 == nil { output = nil } })) { $0 }
    }

    @ViewBuilder private var usageCards: some View {
        if let usageError {
            EmptyStateCard(eyebrow: "DISK USAGE UNAVAILABLE", title: "This engine did not report its disk usage",
                           detail: usageError, symbol: "internaldrive", accent: accent)
        } else if !usage.isEmpty {
            HStack(spacing: 14) {
                ForEach(usage) { item in
                    MetricCard(title: item.type, value: item.size, symbol: symbol(item.type),
                               subtitle: "\(item.active) of \(item.totalCount) in use · \(item.reclaimable) reclaimable",
                               progress: item.totalCount > 0 ? Double(item.active) / Double(item.totalCount) : nil)
                }
            }
        }
    }

    private var pruneCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: "Clean up", detail: "Named volumes and tagged images are never pruned here", symbol: "wand.and.stars")
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 220), spacing: 10)], spacing: 10) {
                ForEach(StoragePruneScope.allCases) { scope in
                    Button { pruning = scope } label: {
                        HStack(alignment: .top, spacing: 10) {
                            Image(systemName: scope.symbol).foregroundStyle(accent).frame(width: 20)
                            VStack(alignment: .leading, spacing: 3) {
                                Text(busy == scope.rawValue ? "Removing…" : scope.title).font(.system(size: 13, weight: .semibold, design: .rounded))
                                Text(scope.removes).font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.leading)
                            }
                            Spacer(minLength: 0)
                        }
                        .padding(12)
                        .frame(maxWidth: .infinity, minHeight: 78, alignment: .topLeading)
                        .insetSurface(cornerRadius: 12)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
            .disabled(busy != nil)
        }
        .padding(20)
        .surface(cornerRadius: 20)
    }

    private var foldersCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: "On this Mac", detail: "Folders where container engines keep their data", symbol: "folder")
            VStack(spacing: 4) {
                ForEach(folders) { item in
                    ResourceRow(symbol: "folder.fill", title: item.name, detail: item.path) {
                        Text(item.size).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                        Button { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: item.path)]) } label: { Image(systemName: "magnifyingglass") }
                            .buttonStyle(.plain).disabled(!item.canReveal).help("Reveal in Finder")
                        Button(busy == item.id ? "Cleaning…" : item.cleanupLabel) { cleaning = item }
                            .buttonStyle(SecondaryButtonStyle())
                            .disabled(!item.canClean || busy != nil)
                            .help(item.canClean ? "Remove unused data from this engine" : "Start this engine to clean its unused data")
                    }
                    .help(item.description)
                }
            }
        }
        .padding(20)
        .surface(cornerRadius: 20)
    }

    private func symbol(_ type: String) -> String {
        switch type {
        case "Images": "square.stack.3d.up.fill"
        case "Containers": "shippingbox.fill"
        case "Local Volumes": "externaldrive.fill"
        case "Build Cache": "hammer.fill"
        default: "internaldrive"
        }
    }

    private func pruneMessage(_ scope: StoragePruneScope) -> String {
        let stopped = model.containers.filter { !$0.isRunning }.map(\.name)
        guard scope == .system || scope == .containers, !stopped.isEmpty else { return scope.removes }
        let more = stopped.count > 8 ? " and \(stopped.count - 8) more" : ""
        return scope.removes + "\n\nStopped containers: " + stopped.prefix(8).joined(separator: ", ") + more
    }

    private func cleanMessage(_ item: StorageHostItem) -> String {
        var text = "\(item.size) on this Mac.\n\nBuild cache only: cached build layers.\nUnused resources: stopped containers, unused networks, dangling images and build cache.\nDeep cleanup: also every unused image and anonymous volume; images may need pulling again.\n\nRunning containers and named volumes are always kept."
        if !item.engineRunning { text += " Its engine is stopped: Dockiva starts it first." }
        return text
    }

    private func present<T>(_ item: Binding<T?>) -> Binding<Bool> {
        Binding(get: { item.wrappedValue != nil }, set: { if !$0 { item.wrappedValue = nil } })
    }

    // MARK: Actions

    private func load() async {
        guard let core = model.core else { return }
        busy = "load"
        defer { busy = nil }
        async let hostItems = try? core.call("DockerService.GetHostContainerStorage", as: [StorageHostItem]?.self)
        do {
            usage = try await core.call("DockerService.GetDiskUsage", as: [StorageDiskUsageItem]?.self) ?? []
            usageError = nil
        } catch {
            usage = []
            usageError = error.localizedDescription
        }
        folders = await hostItems ?? []
    }

    private func prune(_ scope: StoragePruneScope) async {
        busy = scope.rawValue
        let result = await model.callOrReport("DockerService.PruneDocker", [scope.rawValue], as: DockerCLIResult.self)
        busy = nil
        if let text = result?.output, !text.isEmpty { output = text }
        await model.refreshResources()
        await load()
    }

    private func clean(_ item: StorageHostItem, mode: String) async {
        busy = item.id
        let result = await model.callOrReport("DockerService.CleanHostContainerStorage", [item.id, mode], as: DockerCLIResult.self)
        busy = nil
        if let text = result?.output, !text.isEmpty { output = text }
        await load()
    }
}
