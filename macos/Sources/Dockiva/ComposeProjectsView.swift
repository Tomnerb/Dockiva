import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// Compose projects, found from the containers' Compose labels: the projects on
/// the left, the selected one's services, files, actions and logs on the right.
/// A compose.yaml or project folder dropped anywhere on it is imported and started.
@MainActor
struct ComposeProjectsView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dockivaAccent) private var accent
    @State private var selection: String?
    @State private var search = ""
    @State private var hovered: String?
    @State private var importing = false
    @State private var dropTargeted = false
    @State private var output: String?

    private var projects: [(name: String, containers: [ContainerInfo])] {
        Dictionary(grouping: model.containers) { $0.composeProject ?? "" }
            .filter { !$0.key.isEmpty }
            .map { (name: $0.key, containers: $0.value) }
            .sorted { $0.name < $1.name }
    }

    private var rows: [(name: String, containers: [ContainerInfo])] {
        guard !search.isEmpty else { return projects }
        return projects.filter { project in
            project.name.localizedCaseInsensitiveContains(search)
                || project.containers.contains { ($0.composeService ?? $0.name).localizedCaseInsensitiveContains(search) }
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            listPanel
                .frame(width: 330)
                .frame(maxHeight: .infinity, alignment: .top)
                .background(.thinMaterial)
            Rectangle().fill(.white.opacity(0.07)).frame(width: 1)
            Group {
                if let project = projects.first(where: { $0.name == selection }) {
                    ComposeProjectDetail(project: project.name, containers: project.containers, output: $output).id(project.name)
                } else {
                    ScrollView {
                        EmptyStateCard(eyebrow: projects.isEmpty ? "NO PROJECTS YET" : "PICK ONE",
                                       title: projects.isEmpty ? "No Compose projects" : "Select a project",
                                       detail: projects.isEmpty
                                           ? "Import a compose.yaml or a project folder, drop one here, or run docker compose up; projects show up here right away."
                                           : "See its services, files and logs, and bring it up, stop, restart or take it down.",
                                       symbol: "square.stack.3d.forward.dottedline", accent: accent)
                            .padding(28)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .overlay { if dropTargeted { dropHint } }
        .dropDestination(for: URL.self) { urls, _ in
            guard let url = urls.first(where: \.isFileURL), !importing else { return false }
            importCompose(url)
            return true
        } isTargeted: { dropTargeted = $0 }
        .sheet(item: Binding(get: { output.map(OutputSheet.init) }, set: { if $0 == nil { output = nil } })) { $0 }
        .onAppear { if selection == nil { selection = projects.first(where: { $0.containers.contains(where: \.isRunning) })?.name ?? projects.first?.name } }
    }

    private var listPanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Image(systemName: "square.stack.3d.forward.dottedline")
                    .font(.title3)
                    .foregroundStyle(.white)
                    .frame(width: 40, height: 40)
                    .background(accent, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .shadow(color: accent.opacity(0.3), radius: 10, y: 4)
                VStack(alignment: .leading, spacing: 1) {
                    Text("Compose").font(.title2.bold())
                    Text("\(projects.filter { $0.containers.contains(where: \.isRunning) }.count) running · \(projects.count) projects")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(.top, 14)
            Button(action: chooseCompose) {
                HStack(spacing: 8) {
                    if importing { ProgressView().controlSize(.small) } else { Image(systemName: "square.and.arrow.down") }
                    Text(importing ? "Importing…" : "Import Compose…")
                }
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(PrimaryButtonStyle(color: accent))
            .disabled(importing)
            .help("Choose a compose.yaml or a project folder; Dockiva runs docker compose up -d for it")
            SearchField(prompt: "Search projects", text: $search)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 4) {
                    ForEach(rows, id: \.name) { row($0.name, $0.containers) }
                }
                .padding(.vertical, 3)
            }
            .scrollIndicators(.hidden)
            .overlay {
                if rows.isEmpty && !projects.isEmpty {
                    Text("No matching projects").font(.subheadline).foregroundStyle(.secondary).padding(.top, 30)
                }
            }
            Label("Drop a compose.yaml or folder here to import it", systemImage: "arrow.down.doc")
                .font(.caption2).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 18)
        .padding(.bottom, 12)
    }

    private func row(_ name: String, _ containers: [ContainerInfo]) -> some View {
        let isSelected = selection == name
        let running = containers.filter(\.isRunning).count
        let services = Set(containers.map { $0.composeService ?? $0.name }).sorted().joined(separator: ", ")
        return Button { selection = name } label: {
            HStack(spacing: 10) {
                StatusDot(running: running > 0)
                VStack(alignment: .leading, spacing: 1) {
                    Text(name).font(.system(size: 13, weight: .semibold, design: .rounded)).lineLimit(1)
                    Text(services).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 4)
                Text("\(running)/\(containers.count)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    .help("\(running) of \(containers.count) containers running")
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background {
                RoundedRectangle(cornerRadius: 11, style: .continuous)
                    .fill(isSelected ? accent.opacity(0.22) : hovered == name ? Color.white.opacity(0.06) : .clear)
                    .overlay(RoundedRectangle(cornerRadius: 11, style: .continuous).stroke(isSelected ? accent.opacity(0.5) : .clear))
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 ? name : (hovered == name ? nil : hovered) }
    }

    private var dropHint: some View {
        RoundedRectangle(cornerRadius: 20, style: .continuous)
            .fill(accent.opacity(0.1))
            .overlay(RoundedRectangle(cornerRadius: 20, style: .continuous).stroke(accent, style: StrokeStyle(lineWidth: 2, dash: [8, 6])))
            .overlay(Label("Drop to import and start this Compose project", systemImage: "square.and.arrow.down").font(.headline))
            .padding(12)
            .allowsHitTesting(false)
    }

    private func chooseCompose() {
        let panel = NSOpenPanel()
        panel.title = "Import a Compose project"
        panel.message = "Choose a compose.yaml, or a folder that contains one."
        panel.prompt = "Import"
        panel.canChooseFiles = true
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.allowedContentTypes = [.yaml]
        if panel.runModal() == .OK, let url = panel.url { importCompose(url) }
    }

    /// ImportComposePath runs docker compose up -d for the file or folder.
    private func importCompose(_ url: URL) {
        guard let core = model.core, !importing else { return }
        importing = true
        Task {
            do {
                let result = try await core.call("DockerService.ImportComposePath", [url.path], as: ComposeImportResult.self)
                await model.refreshResources()
                selection = result.project
                output = "Imported \(result.project) from \(result.workingDir).\n\n\(result.output)"
            } catch {
                model.lastError = error.localizedDescription
            }
            importing = false
        }
    }
}

/// One project: actions, services (pick one for its logs), its files on disk.
@MainActor
private struct ComposeProjectDetail: View {
    let project: String
    let containers: [ContainerInfo]
    @Binding var output: String?
    @Environment(AppModel.self) private var model
    @Environment(\.dockivaAccent) private var accent
    @State private var details: ComposeProjectDetails?
    @State private var detailsError: String?
    @State private var working: String?  // the action in flight
    @State private var confirm: String?  // "down" or "rebuild"
    @State private var logsFor: ContainerInfo.ID?

    private var running: Int { containers.filter(\.isRunning).count }
    private var serviceCount: Int { Set(containers.map { $0.composeService ?? $0.name }).count }
    private var sorted: [ContainerInfo] {
        containers.sorted { ($0.composeService ?? $0.name, $0.name) < ($1.composeService ?? $1.name, $1.name) }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                FeatureHeader(title: project, subtitle: "\(running) of \(containers.count) containers running  ·  \(serviceCount) services",
                              symbol: "square.stack.3d.forward.dottedline", accent: accent) {
                    EmptyView()
                }
                actions
                HStack(alignment: .top, spacing: 14) {
                    servicesCard
                    filesCard.frame(width: 340)
                }
                logsCard
            }
            .padding(28)
        }
        .task { await loadDetails() }
        .confirmationDialog(confirm == "down" ? "Take \(project) down?" : "Rebuild \(project)?",
                            isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } }),
                            titleVisibility: .visible, presenting: confirm) { action in
            Button(action == "down" ? "Down" : "Rebuild", role: action == "down" ? .destructive : nil) { compose(action) }
            Button("Cancel", role: .cancel) {}
        } message: { action in
            Text(action == "down"
                 ? "Runs docker compose down: the project's containers and networks are removed. Named volumes are kept."
                 : "Runs docker compose build, then docker compose up -d.")
        }
    }

    private func loadDetails() async {
        guard let core = model.core else { return }
        do {
            details = try await core.call("DockerService.GetComposeProjectDetails", [project], as: ComposeProjectDetails.self)
            detailsError = nil
        } catch {
            detailsError = error.localizedDescription
        }
    }

    private var actions: some View {
        HStack(spacing: 8) {
            Button { compose("up") } label: { Label("Up", systemImage: "play.fill") }
                .buttonStyle(PrimaryButtonStyle(color: accent))
                .help("docker compose up -d: create and start every service")
            if running < containers.count {
                Button { projectAction("start") } label: { Label("Start", systemImage: "play") }
                    .buttonStyle(SecondaryButtonStyle())
                    .help("Start the stopped containers")
            }
            if running > 0 {
                Button { projectAction("restart") } label: { Label("Restart", systemImage: "arrow.clockwise") }
                    .buttonStyle(SecondaryButtonStyle())
                Button { projectAction("stop") } label: { Label("Stop", systemImage: "stop.fill") }
                    .buttonStyle(PrimaryButtonStyle(color: .orange))
            }
            Button { confirm = "down" } label: { Label("Down", systemImage: "arrow.down.to.line") }
                .buttonStyle(SecondaryButtonStyle(destructive: true))
                .help("docker compose down: remove the containers and networks")
            Menu {
                Button("Build") { compose("build") }
                Button("Rebuild and recreate…") { confirm = "rebuild" }
                Divider()
                Button("Copy project name") { copy(project) }
            } label: { Image(systemName: "ellipsis.circle") }
                .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(width: 28)
            if let working {
                ProgressView().controlSize(.small).padding(.leading, 4)
                Text("\(working.capitalized)…").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .disabled(working != nil)
    }

    /// Start, stop or restart the project's existing containers.
    private func projectAction(_ action: String) {
        working = action
        Task {
            await model.perform("DockerService.ComposeProjectAction", [project, action])
            working = nil
        }
    }

    /// docker compose up, down, build or rebuild in the project's directory; shows its output.
    private func compose(_ action: String) {
        working = action
        Task {
            if let result = await model.cliAction("DockerService.RunComposeCommand", [project, action]) {
                output = result.isEmpty ? "docker compose \(action) finished for \(project)." : result
            }
            working = nil
        }
    }

    private var servicesCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: "Services", detail: "Pick one to see its logs below", symbol: "square.stack.3d.up")
            VStack(spacing: 2) {
                ForEach(sorted) { serviceRow($0) }
            }
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(cornerRadius: 20)
    }

    private func serviceRow(_ c: ContainerInfo) -> some View {
        let isSelected = (logsFor ?? sorted.first?.id) == c.id
        return Button { logsFor = c.id } label: {
            HStack(spacing: 10) {
                StatusDot(running: c.isRunning)
                VStack(alignment: .leading, spacing: 1) {
                    Text(c.composeService ?? c.name).font(.system(size: 13, weight: .semibold, design: .rounded)).lineLimit(1)
                    Text("\(c.name) · \(c.image)").font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
                Spacer(minLength: 8)
                if model.busy.contains(c.id) { ProgressView().controlSize(.small) }
                Text(c.status).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(isSelected ? accent.opacity(0.18) : .clear, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .contextMenu { ContainerMenu(container: c) }
    }

    private var filesCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionTitle(title: "Project files", detail: "Where Compose runs it from", symbol: "folder")
            if let details {
                if !details.workingDir.isEmpty { fileRow("Working directory", details.workingDir) }
                ForEach(details.configFiles ?? [], id: \.self) { fileRow("Compose file", $0) }
                if let env = details.envFile, !env.isEmpty { fileRow("Env file", env) }
                if details.workingDir.isEmpty && (details.configFiles ?? []).isEmpty {
                    Text("Compose did not record this project's files, so Up, Down and Build cannot run. Start, Stop and Restart still work.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } else if let detailsError {
                Label(detailsError, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange).textSelection(.enabled)
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(cornerRadius: 20)
    }

    private func fileRow(_ label: String, _ path: String) -> some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(label).font(.caption2.weight(.bold)).foregroundStyle(.secondary)
                Text(path).font(.system(.caption, design: .monospaced)).lineLimit(1).truncationMode(.head)
                    .textSelection(.enabled).help(path)
            }
            Spacer(minLength: 4)
            Button { NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath: "") } label: { Image(systemName: "folder") }
                .buttonStyle(.plain).help("Show in Finder")
            Button { copy(path) } label: { Image(systemName: "doc.on.doc") }
                .buttonStyle(.plain).help("Copy path")
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .insetSurface(cornerRadius: 10)
    }

    private var logsCard: some View {
        let target = containers.first { $0.id == logsFor } ?? sorted.first
        return VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: "Logs", detail: target.map { "\($0.composeService ?? $0.name) · live output, last 500 lines" } ?? "No containers",
                         symbol: "text.alignleft")
            if let target {
                LogsView(containerID: target.id)
                    .id(target.id)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
        }
        .padding(18)
        .frame(height: 380)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(cornerRadius: 20)
    }
}
