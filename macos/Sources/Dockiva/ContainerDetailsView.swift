import AppKit
import SwiftUI

/// A container's inspect data (GetContainerDetails): overview, process,
/// environment, ports, mounts, networks and labels. The Details tab of ContainerDetail.
@MainActor
struct ContainerDetailsView: View {
    let containerID: String
    @Environment(AppModel.self) private var model
    @Environment(\.dockivaAccent) private var accent
    @State private var details: ContainerDetails?
    @State private var loadError: String?
    @State private var reveal = false
    @State private var envFilter = ""

    private var container: ContainerInfo? { model.containers.first { $0.id == containerID } }

    var body: some View {
        Group {
            if let details {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        overview(details)
                        process(details)
                        environment(details)
                        ports
                        mounts(details.mounts ?? [])
                        networks(details.networks ?? [])
                        labels(details.labels ?? [:])
                    }
                }
                .scrollIndicators(.automatic)
            } else if let loadError {
                Label(loadError, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange).textSelection(.enabled)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        // Reload when the container starts or stops: the times, state and IPs change.
        .task(id: "\(containerID)|\(container?.state ?? "")") { await load() }
    }

    private func load() async {
        guard let core = model.core else { return }
        do {
            details = try await core.call("DockerService.GetContainerDetails", [containerID], as: ContainerDetails.self)
            loadError = nil
        } catch {
            loadError = error.localizedDescription
        }
    }

    private func overview(_ d: ContainerDetails) -> some View {
        var rows: [(String, String)] = [
            ("Image", container?.image ?? d.image),
            ("Image ID", d.image),
            ("State", d.state == "running" || d.state == "created" ? d.state : "\(d.state) (exit code \(d.exitCode))"),
        ]
        if let status = container?.status, status != d.state { rows.append(("Status", status)) }  // includes health on Docker
        rows += [
            ("Restart policy", d.restartPolicy.isEmpty ? "no" : d.restartPolicy),
            ("User", d.user.isEmpty ? "image default" : d.user),
            ("Working dir", d.workingDir.isEmpty ? "/" : d.workingDir),
            ("Created", time(d.created)),
            ("Started", time(d.startedAt)),
        ]
        if d.state != "running", ContainerDetails.date(d.finishedAt) != nil { rows.append(("Finished", time(d.finishedAt))) }
        return DetailsSection(title: "Overview", detail: "\(d.id.prefix(12)) · \(d.name)", symbol: "info.circle") {
            KeyValueGrid(rows: rows)
        }
    }

    private func time(_ value: String) -> String {
        guard let date = ContainerDetails.date(value) else { return "—" }
        return "\(date.formatted(date: .abbreviated, time: .shortened))  (\(date.formatted(.relative(presentation: .named))))"
    }

    private func process(_ d: ContainerDetails) -> some View {
        let full = ContainerDetails.commandLine((d.entrypoint ?? []) + (d.command ?? []))
        return DetailsSection(title: "Process", detail: "What the container runs", symbol: "terminal") {
            Button { copy(full) } label: { Label("Copy", systemImage: "doc.on.doc") }
                .buttonStyle(SecondaryButtonStyle())
                .disabled(full.isEmpty)
        } content: {
            KeyValueGrid(rows: [
                ("Entrypoint", d.entrypoint?.isEmpty == false ? ContainerDetails.commandLine(d.entrypoint) : "—"),
                ("Command", d.command?.isEmpty == false ? ContainerDetails.commandLine(d.command) : "—"),
            ])
        }
    }

    private func environment(_ d: ContainerDetails) -> some View {
        let pairs = d.environmentPairs.filter {
            envFilter.isEmpty || $0.key.localizedCaseInsensitiveContains(envFilter) || (reveal && $0.value.localizedCaseInsensitiveContains(envFilter))
        }
        return DetailsSection(title: "Environment", detail: "\(d.environment?.count ?? 0) variables", symbol: "list.bullet.rectangle") {
            HStack(spacing: 8) {
                SearchField(prompt: "Filter", text: $envFilter).frame(width: 170)
                Button { reveal.toggle() } label: { Label(reveal ? "Hide values" : "Show values", systemImage: reveal ? "eye.slash" : "eye") }
                    .buttonStyle(SecondaryButtonStyle())
            }
        } content: {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(pairs.enumerated()), id: \.offset) { _, pair in envRow(pair.key, pair.value) }
                if pairs.isEmpty { emptyLine(d.environment?.isEmpty == false ? "No matching variables." : "No environment variables.") }
            }
        }
    }

    private func envRow(_ key: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(key).foregroundStyle(accent)
            Text("=").foregroundStyle(.secondary)
            Text(reveal ? value : "••••••••").foregroundStyle(reveal ? .primary : .secondary).textSelection(.enabled)
            Spacer(minLength: 8)
            Button { copy(value) } label: { Image(systemName: "doc.on.doc") }
                .buttonStyle(.plain).foregroundStyle(.secondary).help("Copy value")
        }
        .font(.system(.callout, design: .monospaced))
        .padding(.vertical, 3)
        .contextMenu {
            Button("Copy value") { copy(value) }
            Button("Copy \(key)=…") { copy("\(key)=\(value)") }
        }
    }

    private var ports: some View {
        let list = container?.ports ?? []
        return DetailsSection(title: "Ports", detail: list.isEmpty ? "None exposed" : "\(list.count) exposed", symbol: "point.3.connected.trianglepath.dotted") {
            if list.isEmpty { emptyLine("No ports.") }
            ForEach(list, id: \.self) { port in
                HStack {
                    Text(port.display).font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                    Spacer()
                    if let url = port.url.flatMap(URL.init(string:)) {
                        Button { NSWorkspace.shared.open(url) } label: { Label(url.absoluteString, systemImage: "arrow.up.right.square") }
                            .buttonStyle(.plain).foregroundStyle(accent)
                    }
                }
            }
        }
    }

    private func mounts(_ list: [ContainerDetails.Mount]) -> some View {
        DetailsSection(title: "Mounts", detail: "\(list.count) mounts", symbol: "externaldrive") {
            if list.isEmpty { emptyLine("No mounts.") }
            ForEach(Array(list.enumerated()), id: \.offset) { _, mount in
                HStack(spacing: 10) {
                    Text(mount.type).font(.caption2.weight(.bold)).padding(.horizontal, 7).padding(.vertical, 3)
                        .background(accent.opacity(0.18), in: Capsule())
                        .help(mount.name ?? "")
                    VStack(alignment: .leading, spacing: 2) {
                        Text(mount.destination).font(.system(.callout, design: .monospaced).weight(.semibold))
                        Text(mount.source).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
                            .lineLimit(1).truncationMode(.middle).help(mount.source)
                    }
                    .textSelection(.enabled)
                    Spacer(minLength: 8)
                    Text(mount.rw ? "read-write" : "read-only").font(.caption).foregroundStyle(.secondary)
                    if FileManager.default.fileExists(atPath: mount.source) {
                        Button { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: mount.source) } label: { Image(systemName: "folder") }
                            .buttonStyle(.plain).help("Show in Finder")
                    }
                }
            }
        }
    }

    private func networks(_ list: [ContainerDetails.Network]) -> some View {
        DetailsSection(title: "Networks", detail: "\(list.count) networks", symbol: "network") {
            if list.isEmpty { emptyLine("Not attached to a network.") }
            ForEach(list, id: \.name) { network in
                VStack(alignment: .leading, spacing: 4) {
                    Text(network.name).font(.subheadline.weight(.semibold))
                    KeyValueGrid(rows: [
                        ("IP address", network.ipAddress.isEmpty ? "—" : network.ipAddress),
                        ("Gateway", network.gateway.isEmpty ? "—" : network.gateway),
                        ("MAC", network.mac.isEmpty ? "—" : network.mac),
                    ])
                }
            }
        }
    }

    private func labels(_ labels: [String: String]) -> some View {
        DetailsSection(title: "Labels", detail: "\(labels.count) labels", symbol: "tag") {
            if labels.isEmpty { emptyLine("No labels.") }
            ForEach(labels.keys.sorted(), id: \.self) { key in
                (Text(key).foregroundColor(accent) + Text(" = ").foregroundColor(.secondary) + Text(labels[key] ?? ""))
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private func emptyLine(_ text: String) -> some View {
        Text(text).font(.callout).foregroundStyle(.secondary)
    }
}

/// A titled section in an inset surface, with optional trailing controls.
private struct DetailsSection<Trailing: View, Content: View>: View {
    let title: String
    let detail: String
    let symbol: String
    @ViewBuilder let trailing: Trailing
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                SectionTitle(title: title, detail: detail, symbol: symbol)
                Spacer(minLength: 12)
                trailing
            }
            content
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .insetSurface()
    }
}

extension DetailsSection where Trailing == EmptyView {
    init(title: String, detail: String, symbol: String, @ViewBuilder content: () -> Content) {
        self.init(title: title, detail: detail, symbol: symbol, trailing: { EmptyView() }, content: content)
    }
}

private struct KeyValueGrid: View {
    let rows: [(String, String)]

    var body: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 7) {
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                GridRow {
                    Text(row.0).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Text(row.1).font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }
}
