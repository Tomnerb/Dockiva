import AppKit
import SwiftUI

struct ContainersView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: ContainerInfo.ID?
    @State private var search = ""
    @State private var inspector = false

    private var rows: [ContainerInfo] {
        guard !search.isEmpty else { return model.containers }
        return model.containers.filter {
            $0.name.localizedCaseInsensitiveContains(search) || $0.image.localizedCaseInsensitiveContains(search)
                || ($0.composeProject ?? "").localizedCaseInsensitiveContains(search)
        }
    }

    private var selected: ContainerInfo? { model.containers.first { $0.id == selection } }

    var body: some View {
        Table(rows, selection: $selection) {
            TableColumn("Name") { c in nameCell(c) }.width(min: 110, ideal: 220)
            TableColumn("Image") { c in Text(c.image).lineLimit(1).truncationMode(.middle) }.width(min: 70, ideal: 180)
            TableColumn("Status") { c in Text(c.status).lineLimit(1) }.width(min: 60, ideal: 130)
            TableColumn("CPU") { c in Text(cpu(c)).monospacedDigit() }.width(56)
            TableColumn("Memory") { c in Text(memory(c)).monospacedDigit() }.width(76)
            TableColumn("Ports") { c in portsCell(c) }.width(min: 60, ideal: 150)
        }
        .contextMenu(forSelectionType: ContainerInfo.ID.self) { ids in
            if let c = model.containers.first(where: { ids.contains($0.id) }) { actions(for: c) }
        }
        .searchable(text: $search, prompt: "Filter containers")
        .onChange(of: selection) { if selection != nil { inspector = true } }
        .overlay {
            if model.containers.isEmpty {
                ContentUnavailableView("No containers", systemImage: "shippingbox", description: Text("Run one with docker run, or start the engine."))
            }
        }
        .toolbar { toolbar }
        .inspector(isPresented: $inspector) {
            Group {
                if let c = selected {
                    ContainerInspector(container: c).id(c.id)
                } else {
                    ContentUnavailableView("Select a container", systemImage: "sidebar.trailing")
                }
            }
            .inspectorColumnWidth(min: 260, ideal: 360, max: 900)
        }
        .navigationTitle("Containers")
        .navigationSubtitle("\(model.runningCount) running")
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        ToolbarItemGroup {
            if let c = selected {
                containerButtons(c).disabled(model.busy.contains(c.id))
            }
            Button { inspector.toggle() } label: { Label("Inspector", systemImage: "sidebar.trailing") }
        }
    }

    @ViewBuilder private func containerButtons(_ c: ContainerInfo) -> some View {
        if c.isRunning {
            Button { act("stop", c) } label: { Label("Stop", systemImage: "stop.fill") }
            Button { act("restart", c) } label: { Label("Restart", systemImage: "arrow.clockwise") }
        } else {
            Button { act("start", c) } label: { Label("Start", systemImage: "play.fill") }
        }
        Button(role: .destructive) { act("remove", c) } label: { Label("Remove", systemImage: "trash") }
    }

    private func act(_ action: String, _ c: ContainerInfo) {
        Task { await model.containerAction(action, c) }
    }

    private func cpu(_ c: ContainerInfo) -> String {
        guard let stats = model.stats[c.id] else { return "—" }
        return String(format: "%.1f%%", stats.cpuPercent)
    }

    private func memory(_ c: ContainerInfo) -> String {
        model.stats[c.id]?.memoryUsage.byteString ?? "—"
    }

    private func nameCell(_ c: ContainerInfo) -> some View {
        HStack(spacing: 6) {
            Circle().fill(c.isRunning ? Color.green : Color.secondary.opacity(0.5)).frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                Text(c.name).lineLimit(1)
                if let project = c.composeProject { Text(project).font(.caption).foregroundStyle(.secondary) }
            }
        }
    }

    /// The first published port (a link when it has a URL), and how many more.
    @ViewBuilder private func portsCell(_ c: ContainerInfo) -> some View {
        let ports = (c.ports ?? []).filter { $0.publicPort > 0 }
        if let first = ports.first {
            HStack(spacing: 4) {
                if let url = first.url.flatMap(URL.init(string:)) {
                    Link(first.display, destination: url).lineLimit(1)
                } else {
                    Text(first.display).lineLimit(1)
                }
                if ports.count > 1 { Text("+\(ports.count - 1)").foregroundStyle(.secondary) }
            }
            .help(ports.map(\.display).joined(separator: "\n"))
        }
    }

    @ViewBuilder private func actions(for c: ContainerInfo) -> some View {
        if c.isRunning {
            Button("Stop") { Task { await model.containerAction("stop", c) } }
            Button("Restart") { Task { await model.containerAction("restart", c) } }
        } else {
            Button("Start") { Task { await model.containerAction("start", c) } }
        }
        Button("Copy ID") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(c.id, forType: .string)
        }
        Divider()
        Button("Remove", role: .destructive) { Task { await model.containerAction("remove", c) } }
    }
}

struct ContainerInspector: View {
    let container: ContainerInfo
    @State private var tab = "Logs"

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                Text(container.name).font(.headline)
                Text(container.image).font(.caption).foregroundStyle(.secondary)
                if let ip = container.ipAddress, !ip.isEmpty { Text("IP \(ip)").font(.caption).foregroundStyle(.secondary) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            Picker("", selection: $tab) {
                Text("Logs").tag("Logs")
                Text("Terminal").tag("Terminal")
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(.horizontal, 12)
            .padding(.bottom, 8)
            Divider()
            if tab == "Logs" {
                LogsView(containerID: container.id)
            } else if container.isRunning {
                TerminalView(containerID: container.id)
            } else {
                ContentUnavailableView("Container is stopped", systemImage: "terminal", description: Text("Start it to open a terminal."))
            }
        }
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
        .background(Color(nsColor: .textBackgroundColor))
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
