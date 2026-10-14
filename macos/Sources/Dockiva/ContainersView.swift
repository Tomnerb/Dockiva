import AppKit
import SwiftUI

struct ContainersView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: ContainerInfo.ID?
    @State private var search = ""
    @State private var filter = "all"
    @State private var hovered: ContainerInfo.ID?

    private let accent = SidebarItem.containers.accent

    private var rows: [ContainerInfo] {
        model.containers.filter { c in
            (filter == "all" || (filter == "running") == c.isRunning)
                && (search.isEmpty || c.name.localizedCaseInsensitiveContains(search) || c.image.localizedCaseInsensitiveContains(search)
                    || (c.composeProject ?? "").localizedCaseInsensitiveContains(search))
        }
    }

    /// Rows grouped by Compose project; standalone containers last.
    private var groups: [(project: String?, items: [ContainerInfo])] {
        let grouped = Dictionary(grouping: rows) { $0.composeProject }
        return grouped.keys.sorted { ($0 ?? "\u{10FFFF}") < ($1 ?? "\u{10FFFF}") }.map { key in
            (key, grouped[key]!.sorted { ($0.isRunning ? 0 : 1, $0.name) < ($1.isRunning ? 0 : 1, $1.name) })
        }
    }

    private var selected: ContainerInfo? { model.containers.first { $0.id == selection } }

    var body: some View {
        HStack(spacing: 0) {
            listPanel
                .frame(width: 330)
                .frame(maxHeight: .infinity, alignment: .top)
                .background(.thinMaterial)
            Rectangle().fill(.white.opacity(0.07)).frame(width: 1)
            Group {
                if let c = selected {
                    ContainerDetail(container: c).id(c.id)
                } else {
                    ScrollView {
                        EmptyStateCard(eyebrow: "PICK ONE", title: model.containers.isEmpty ? "No containers yet" : "Select a container",
                                       detail: model.containers.isEmpty
                                           ? "Run one with docker run or docker compose up; it shows up here right away."
                                           : "See its live usage, logs and a terminal, and start, stop or remove it.",
                                       symbol: "shippingbox.fill", accent: accent)
                            .padding(28)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear { if selection == nil { selection = model.containers.first(where: \.isRunning)?.id } }
    }

    private var listPanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Image(systemName: "shippingbox.fill")
                    .font(.title3)
                    .foregroundStyle(.white)
                    .frame(width: 40, height: 40)
                    .background(accent, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .shadow(color: accent.opacity(0.3), radius: 10, y: 4)
                VStack(alignment: .leading, spacing: 1) {
                    Text("Containers").font(.title2.bold())
                    Text("\(model.runningCount) running · \(model.containers.count) total").font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(.top, 14)
            SearchField(prompt: "Search containers", text: $search)
            FilterPills(choices: [("All", "all"), ("Running", "running"), ("Stopped", "stopped")], selection: $filter)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 4) {
                    ForEach(groups, id: \.project) { group in
                        if let project = group.project {
                            Label(project, systemImage: "square.stack.3d.forward.dottedline")
                                .font(.caption.weight(.bold))
                                .foregroundStyle(.secondary)
                                .padding(.top, 8)
                                .padding(.leading, 4)
                        } else if groups.count > 1 {
                            Text("Standalone").font(.caption.weight(.bold)).foregroundStyle(.secondary).padding(.top, 8).padding(.leading, 4)
                        }
                        ForEach(group.items) { row($0) }
                    }
                }
                .padding(.vertical, 3)
            }
            .scrollIndicators(.hidden)
            .overlay {
                if rows.isEmpty && !model.containers.isEmpty {
                    Text("No matching containers").font(.subheadline).foregroundStyle(.secondary).padding(.top, 30)
                }
            }
        }
        .padding(.horizontal, 18)
        .padding(.bottom, 12)
    }

    private func row(_ c: ContainerInfo) -> some View {
        let isSelected = selection == c.id
        return Button { selection = c.id } label: {
            HStack(spacing: 10) {
                StatusDot(running: c.isRunning)
                VStack(alignment: .leading, spacing: 1) {
                    Text(c.composeService ?? c.name).font(.system(size: 13, weight: .semibold, design: .rounded)).lineLimit(1)
                    Text(c.image).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 4)
                if model.busy.contains(c.id) {
                    ProgressView().controlSize(.small)
                } else if let stats = model.stats[c.id], c.isRunning {
                    VStack(alignment: .trailing, spacing: 1) {
                        Text(String(format: "%.1f%%", stats.cpuPercent)).font(.caption.monospacedDigit())
                        Text(stats.memoryUsage.byteString).font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background {
                RoundedRectangle(cornerRadius: 11, style: .continuous)
                    .fill(isSelected ? accent.opacity(0.22) : hovered == c.id ? Color.white.opacity(0.06) : .clear)
                    .overlay(RoundedRectangle(cornerRadius: 11, style: .continuous).stroke(isSelected ? accent.opacity(0.5) : .clear))
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 ? c.id : (hovered == c.id ? nil : hovered) }
        .contextMenu { ContainerMenu(container: c) }
    }
}

struct ContainerMenu: View {
    let container: ContainerInfo
    @Environment(AppModel.self) private var model

    var body: some View {
        if container.isRunning {
            Button("Stop") { Task { await model.containerAction("stop", container) } }
            Button("Restart") { Task { await model.containerAction("restart", container) } }
        } else {
            Button("Start") { Task { await model.containerAction("start", container) } }
        }
        Button("Copy ID") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(container.id, forType: .string)
        }
        Divider()
        Button("Remove", role: .destructive) { Task { await model.containerAction("remove", container) } }
    }
}

struct ContainerDetail: View {
    let container: ContainerInfo
    @Environment(AppModel.self) private var model
    @State private var tab = "logs"
    @State private var confirmRemove = false

    private let accent = SidebarItem.containers.accent
    private var stats: ContainerResourceStats? { model.stats[container.id] }
    private var busy: Bool { model.busy.contains(container.id) }
    private var published: [PortInfo] { (container.ports ?? []).filter { $0.publicPort > 0 } }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            FeatureHeader(title: container.name, subtitle: subtitle, symbol: container.isRunning ? "shippingbox.fill" : "shippingbox", accent: accent) {
                EmptyView()
            }
            HStack(spacing: 8) {
                actions
                Spacer(minLength: 12)
                ports
            }
            HStack(spacing: 14) {
                MetricCard(title: "CPU", value: stats.map { String(format: "%.1f%%", $0.cpuPercent) } ?? "—", symbol: "cpu",
                           subtitle: container.isRunning ? "Of one core = 100%" : "Not running")
                MetricCard(title: "Memory", value: stats?.memoryUsage.byteString ?? "—", symbol: "memorychip",
                           subtitle: stats.map { $0.memoryLimit > 0 ? "Limit \($0.memoryLimit.byteString)" : "No limit" } ?? "Not running",
                           progress: stats.flatMap { $0.memoryLimit > 0 ? Double($0.memoryUsage) / Double($0.memoryLimit) : nil })
                MetricCard(title: "PIDs", value: stats.map { "\($0.pids)" } ?? "—", symbol: "list.number",
                           subtitle: container.status)
            }
            console
        }
        .padding(28)
        .confirmationDialog("Remove \(container.name)?", isPresented: $confirmRemove, titleVisibility: .visible) {
            Button("Remove", role: .destructive) { Task { await model.containerAction("remove", container) } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(container.isRunning ? "The container is running; it will be stopped and removed. Its anonymous volumes are kept." : "Its anonymous volumes are kept.")
        }
    }

    private var subtitle: String {
        var parts = [container.image]
        if let project = container.composeProject { parts.append("Compose: \(project)") }
        if let ip = container.ipAddress, !ip.isEmpty { parts.append(ip) }
        return parts.joined(separator: "  ·  ")
    }

    private var actions: some View {
        HStack(spacing: 8) {
            if busy { ProgressView().controlSize(.small) }
            if container.isRunning {
                Button { act("restart") } label: { Label("Restart", systemImage: "arrow.clockwise") }.buttonStyle(SecondaryButtonStyle())
                Button { act("stop") } label: { Label("Stop", systemImage: "stop.fill") }.buttonStyle(PrimaryButtonStyle(color: .orange))
            } else {
                Button { act("start") } label: { Label("Start", systemImage: "play.fill") }.buttonStyle(PrimaryButtonStyle(color: accent))
            }
            Button { confirmRemove = true } label: { Image(systemName: "trash") }
                .buttonStyle(SecondaryButtonStyle(destructive: true))
                .help("Remove")
        }
        .disabled(busy)
    }

    private func act(_ action: String) { Task { await model.containerAction(action, container) } }

    private var ports: some View {
        HStack(spacing: 8) {
            ForEach(published.prefix(3), id: \.self) { port in
                if let url = port.url.flatMap(URL.init(string:)) {
                    Button { NSWorkspace.shared.open(url) } label: {
                        Label(port.display, systemImage: "arrow.up.right.square").font(.caption.weight(.semibold))
                    }
                    .buttonStyle(SecondaryButtonStyle())
                } else {
                    Text(port.display).font(.caption.monospacedDigit()).padding(.horizontal, 10).padding(.vertical, 6).insetSurface(cornerRadius: 9)
                }
            }
        }
        .fixedSize()
    }

    private var console: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                SectionTitle(title: tab == "logs" ? "Logs" : "Terminal",
                             detail: tab == "logs" ? "Live output, last 500 lines" : "A shell inside the container",
                             symbol: tab == "logs" ? "text.alignleft" : "terminal")
                Spacer()
                FilterPills(choices: [("Logs", "logs"), ("Terminal", "terminal")], selection: $tab)
            }
            Group {
                if tab == "logs" {
                    LogsView(containerID: container.id)
                } else if container.isRunning {
                    TerminalView(containerID: container.id)
                } else {
                    Text("Start the container to open a terminal.").font(.callout).foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .padding(18)
        .frame(maxHeight: .infinity)
        .surface(cornerRadius: 20)
    }
}

/// Text output from a log stream or terminal, trimmed to a bounded size.
@MainActor @Observable
final class StreamBuffer {
    var text = ""
    var ended: String?
    private let limit = 400_000

    func append(_ output: StreamOutputEvent) {
        if let data = output.data { text += stripANSI(data) }
        if text.count > limit { text = String(text.suffix(limit * 3 / 4)) }
        if let error = output.error { ended = error } else if output.closed == true { ended = "Stream ended" }
    }
}

/// Removes terminal escape sequences (colors, cursor movement) for plain-text display.
func stripANSI(_ s: String) -> String {
    s.replacingOccurrences(of: "\u{1B}\\[[0-9;?]*[ -/]*[@-~]|\u{1B}\\][^\u{07}\u{1B}]*(\u{07}|\u{1B}\\\\)|\u{1B}[()][0-9A-Za-z]|\u{1B}[=>]|\r", with: "", options: .regularExpression)
}

struct StreamTextView: View {
    let text: String

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                Text(text.isEmpty ? " " : text)
                    .font(.system(.callout, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                Color.clear.frame(height: 1).id("end")
            }
            .onChange(of: text) { proxy.scrollTo("end", anchor: .bottom) }
            .onAppear { proxy.scrollTo("end", anchor: .bottom) }
        }
        .background(Color.black.opacity(0.35))
    }
}

struct LogsView: View {
    let containerID: String
    @Environment(AppModel.self) private var model
    @State private var buffer = StreamBuffer()
    @State private var streamID: String?

    var body: some View {
        StreamTextView(text: buffer.text)
            .overlay(alignment: .bottom) {
                if let ended = buffer.ended { Text(ended).font(.caption).padding(6).background(.thinMaterial, in: Capsule()).padding(8) }
            }
            .task {
                do {
                    streamID = try await model.openStream("DockerService.StartLogStream", [containerID, 500]) { buffer.append($0) }
                } catch {
                    buffer.ended = error.localizedDescription
                }
            }
            .onDisappear { if let streamID { model.closeStream(streamID, method: "DockerService.StopLogStream") } }
    }
}

/// A line-oriented terminal: output as text, input sent a line (or control key) at a time.
struct TerminalView: View {
    let containerID: String
    @Environment(AppModel.self) private var model
    @State private var buffer = StreamBuffer()
    @State private var sessionID: String?
    @State private var line = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            StreamTextView(text: buffer.text)
            Divider()
            HStack {
                Text("$").font(.system(.body, design: .monospaced)).foregroundStyle(.secondary)
                TextField("Command", text: $line)
                    .textFieldStyle(.plain)
                    .font(.system(.body, design: .monospaced))
                    .focused($focused)
                    .onSubmit { send(line + "\n"); line = "" }
                Button("^C") { send("\u{03}") }.help("Send Ctrl-C")
                Button("Tab") { send(line + "\t"); line = "" }.help("Complete")
            }
            .padding(8)
            .disabled(sessionID == nil || buffer.ended != nil)
        }
        .overlay(alignment: .top) {
            if let ended = buffer.ended { Text(ended).font(.caption).padding(6).background(.thinMaterial, in: Capsule()).padding(8) }
        }
        .task {
            do {
                sessionID = try await model.openStream("DockerService.StartTerminal", [containerID]) { buffer.append($0) }
                focused = true
            } catch {
                buffer.ended = error.localizedDescription
            }
        }
        .onDisappear { if let sessionID { model.closeStream(sessionID, method: "DockerService.CloseTerminal") } }
    }

    private func send(_ input: String) {
        guard let sessionID, let core = model.core else { return }
        Task { try? await core.run("DockerService.SendTerminalInput", [sessionID, input]) }
    }
}
