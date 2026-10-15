import AppKit
import SwiftUI

/// The frame shared by the resource screens: a feature header, an optional bar,
/// then rows in a frosted card.
struct ResourceScreen<Actions: View, Bar: View, Rows: View>: View {
    let item: SidebarItem
    let subtitle: String
    let isEmpty: Bool
    let emptyTitle: String
    let emptyDetail: String
    @ViewBuilder let actions: Actions
    @ViewBuilder let bar: Bar
    @ViewBuilder let rows: Rows

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                FeatureHeader(title: item.title, subtitle: subtitle, symbol: item.symbol, accent: item.accent) { actions }
                bar
                if isEmpty {
                    EmptyStateCard(eyebrow: "NOTHING HERE YET", title: emptyTitle, detail: emptyDetail, symbol: item.symbol, accent: item.accent)
                } else {
                    LazyVStack(spacing: 4) { rows }
                        .padding(10)
                        .surface(cornerRadius: 20)
                }
            }
            .padding(28)
        }
    }
}

/// A list row: icon tile, title and detail, trailing values, hover highlight.
struct ResourceRow<Trailing: View>: View {
    let symbol: String
    let title: String
    let detail: String
    @ViewBuilder let trailing: Trailing
    @Environment(\.dockivaAccent) private var accent
    @State private var hovered = false

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(accent)
                .frame(width: 32, height: 32)
                .background(accent.opacity(0.13), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 14, weight: .semibold, design: .rounded)).lineLimit(1).truncationMode(.middle)
                Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 12)
            trailing
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(hovered ? Color.white.opacity(0.055) : .clear, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .onHover { hovered = $0 }
    }
}

struct ImagesView: View {
    @Environment(AppModel.self) private var model
    @State private var reference = ""
    @State private var pulling = false
    @State private var search = ""
    @State private var output: String?

    private var rows: [ImageInfo] {
        search.isEmpty ? model.images : model.images.filter { $0.title.localizedCaseInsensitiveContains(search) || $0.shortId.contains(search) }
    }

    var body: some View {
        ResourceScreen(item: .images, subtitle: "\(model.images.count) images · \(model.imageBytes.byteString) on disk",
                       isEmpty: model.images.isEmpty, emptyTitle: "No images",
                       emptyDetail: "Pull one above, or run docker pull; images appear here with their size.") {
            EmptyView()
        } bar: {
            HStack(spacing: 10) {
                HStack(spacing: 8) {
                    Image(systemName: "arrow.down.circle").foregroundStyle(.secondary)
                    TextField("Pull an image, e.g. postgres:16-alpine", text: $reference).textFieldStyle(.plain).onSubmit(pull)
                }
                .padding(.horizontal, 11)
                .padding(.vertical, 8)
                .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                Button(action: pull) { Label(pulling ? "Pulling…" : "Pull", systemImage: "arrow.down") }
                    .buttonStyle(PrimaryButtonStyle(color: SidebarItem.images.accent))
                    .disabled(pulling || reference.trimmingCharacters(in: .whitespaces).isEmpty)
                SearchField(prompt: "Filter", text: $search).frame(width: 200)
            }
        } rows: {
            ForEach(rows) { image in
                ResourceRow(symbol: "square.stack.3d.up.fill", title: image.title,
                            detail: "\(image.shortId) · \(Date(timeIntervalSince1970: TimeInterval(image.created)).formatted(.relative(presentation: .named)))") {
                    if image.containers > 0 {
                        Text("in use").font(.caption2.weight(.bold)).padding(.horizontal, 7).padding(.vertical, 3)
                            .background(DockivaPalette.running.opacity(0.18), in: Capsule())
                    }
                    Text(image.size.byteString).font(.callout.monospacedDigit()).foregroundStyle(.secondary).frame(width: 80, alignment: .trailing)
                    Menu {
                        Button("Copy name") { copy(image.title) }
                        Button("Remove", role: .destructive) { Task { await model.perform("DockerService.RemoveImage", [image.id, false]) } }
                        Button("Force remove", role: .destructive) { Task { await model.perform("DockerService.RemoveImage", [image.id, true]) } }
                    } label: { Image(systemName: "ellipsis.circle") }
                        .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(width: 28)
                }
            }
        }
        .sheet(item: Binding(get: { output.map(OutputSheet.init) }, set: { if $0 == nil { output = nil } })) { $0 }
    }

    private func pull() {
        let ref = reference.trimmingCharacters(in: .whitespaces)
        guard !ref.isEmpty else { return }
        pulling = true
        Task {
            if let result = await model.cliAction("DockerService.PullImage", [ref]) { output = result; reference = "" }
            pulling = false
        }
    }
}

struct VolumesView: View {
    @Environment(AppModel.self) private var model
    @State private var newName = ""

    var body: some View {
        ResourceScreen(item: .volumes, subtitle: "Data that outlives containers · \(model.volumes.count) volumes",
                       isEmpty: model.volumes.isEmpty, emptyTitle: "No volumes",
                       emptyDetail: "Create one here, or use -v name:/path with docker run.") {
            EmptyView()
        } bar: {
            CreateBar(prompt: "New volume name", text: $newName, accent: SidebarItem.volumes.accent) {
                let name = newName
                Task { if await model.cliAction("DockerService.CreateVolume", [name, "local"]) != nil { newName = "" } }
            }
        } rows: {
            ForEach(model.volumes) { volume in
                ResourceRow(symbol: "externaldrive.fill", title: volume.name, detail: volume.mountpoint) {
                    Button { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: volume.mountpoint) } label: { Image(systemName: "folder") }
                        .buttonStyle(.plain).help("Show in Finder")
                    Menu {
                        Button("Copy name") { copy(volume.name) }
                        Button("Remove", role: .destructive) { Task { await model.perform("DockerService.RemoveVolume", [volume.name, false]) } }
                    } label: { Image(systemName: "ellipsis.circle") }
                        .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(width: 28)
                }
            }
        }
    }
}

struct NetworksView: View {
    @Environment(AppModel.self) private var model
    @State private var newName = ""

    var body: some View {
        ResourceScreen(item: .networks, subtitle: "Containers on one network reach each other by name",
                       isEmpty: model.networks.isEmpty, emptyTitle: "No networks",
                       emptyDetail: "Create one here, or let docker compose make one per project.") {
            EmptyView()
        } bar: {
            CreateBar(prompt: "New network name", text: $newName, accent: SidebarItem.networks.accent) {
                let name = newName
                Task { if await model.cliAction("DockerService.CreateNetwork", [name, "bridge"]) != nil { newName = "" } }
            }
        } rows: {
            ForEach(model.networks) { network in
                ResourceRow(symbol: "network", title: network.name, detail: "\(network.shortId) · \(network.driver) · \(network.scope)") {
                    if network.internal {
                        Text("internal").font(.caption2.weight(.bold)).padding(.horizontal, 7).padding(.vertical, 3)
                            .background(Color.orange.opacity(0.18), in: Capsule())
                    }
                    Menu {
                        Button("Copy name") { copy(network.name) }
                        Button("Remove", role: .destructive) { Task { await model.perform("DockerService.RemoveNetwork", [network.id]) } }
                    } label: { Image(systemName: "ellipsis.circle") }
                        .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(width: 28)
                }
            }
        }
    }
}

struct CreateBar: View {
    let prompt: String
    @Binding var text: String
    let accent: Color
    let create: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "plus.circle").foregroundStyle(.secondary)
                TextField(prompt, text: $text).textFieldStyle(.plain).onSubmit { if !text.isEmpty { create() } }
            }
            .padding(.horizontal, 11)
            .padding(.vertical, 8)
            .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            Button(action: create) { Label("Create", systemImage: "plus") }
                .buttonStyle(PrimaryButtonStyle(color: accent))
                .disabled(text.isEmpty)
        }
    }
}

struct SettingsToggles: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if let settings = model.settings {
            VStack(alignment: .leading, spacing: 10) {
                Toggle("Start the engine when Dockiva opens", isOn: binding(settings, \.startEngineOnLaunch))
                Toggle("Reconnect to the engine automatically", isOn: binding(settings, \.autoReconnectEngine))
            }
            .toggleStyle(.switch)
        } else {
            ProgressView()
        }
    }

    private func binding(_ settings: AppSettings, _ key: WritableKeyPath<AppSettings, Bool>) -> Binding<Bool> {
        Binding(get: { model.settings?[keyPath: key] ?? settings[keyPath: key] }, set: { value in
            var next = model.settings ?? settings
            next[keyPath: key] = value
            Task { await model.updateSettings(next) }
        })
    }
}

struct SettingsView: View {
    var body: some View {
        Form {
            SettingsToggles()
            StartupSettingsSection()
        }
            .formStyle(.grouped)
            .padding()
    }
}

struct OutputSheet: View, Identifiable {
    let output: String
    @Environment(\.dismiss) private var dismiss
    var id: String { output }

    var body: some View {
        VStack(alignment: .trailing) {
            ScrollView {
                Text(stripANSI(output)).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
        }
        .padding()
        .frame(width: 560, height: 360)
    }
}

func copy(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}
