import SwiftUI

/// Copies images, containers and volumes from the external Docker engine (Docker
/// Desktop, OrbStack…) into Dockiva, following the Vue migration flow: scan,
/// start the source if needed, migrate, watch progress. The source is left unchanged.
@MainActor
struct MigrationView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dockivaAccent) private var accent
    @State private var preview: MigrationPreview?
    @State private var status: MigrationStatus?
    @State private var scanning = false

    private var running: Bool { status?.isRunning == true }
    /// Image and container jobs are tracked by the core; the volume copy is one long call.
    private var polling: Bool { running && status?.kind != "volume" }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                FeatureHeader(title: "Migrate", subtitle: "Bring your containers, images and volumes over from Docker Desktop or OrbStack",
                              symbol: "square.and.arrow.down.on.square.fill", accent: accent) {
                    HStack(spacing: 8) {
                        if scanning { ProgressView().controlSize(.small) }
                        Button { Task { await scan() } } label: { Label("Scan again", systemImage: "arrow.clockwise") }
                            .buttonStyle(SecondaryButtonStyle())
                            .disabled(scanning || running)
                    }
                }
                content
            }
            .padding(28)
        }
        .task {
            status = try? await model.core?.call("DockerService.GetDockerMigrationStatus", as: MigrationStatus.self)
            await scan()
        }
        .task(id: polling) {
            while polling && !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                guard let next = try? await model.core?.call("DockerService.GetDockerMigrationStatus", as: MigrationStatus.self) else { continue }
                status = next
                if next.state == "complete" { await model.refreshResources() }
            }
        }
    }

    @ViewBuilder private var content: some View {
        if model.settings?.engineBackend == "external" {
            EmptyStateCard(eyebrow: "USING DOCKER DIRECTLY", title: "Nothing to migrate",
                           detail: "Dockiva is running your external Docker engine as it is. Choose a Dockiva engine in Engine to copy your Docker data into it.",
                           symbol: "square.and.arrow.down.on.square.fill", accent: accent)
        } else if let preview {
            if preview.reachable {
                sourceCard(preview)
            } else {
                EmptyStateCard(eyebrow: preview.endpoint == nil ? "NOT AVAILABLE" : "SOURCE OFFLINE",
                               title: preview.endpoint == nil ? "Migration is unavailable" : "Docker is not running",
                               detail: preview.message ?? "", symbol: "exclamationmark.triangle.fill", accent: .orange)
                if preview.endpoint != nil {
                    Button { Task { await scan(startSource: true) } } label: {
                        Label(scanning ? "Starting Docker…" : "Start Docker and scan", systemImage: "power")
                    }
                    .buttonStyle(PrimaryButtonStyle(color: accent))
                    .disabled(scanning)
                }
            }
            if let status, !status.state.isEmpty { progressCard(status) }
        } else {
            ProgressView("Looking for Docker…").frame(maxWidth: .infinity).padding(40)
        }
    }

    private func sourceCard(_ preview: MigrationPreview) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            SectionTitle(title: "Found in Docker", detail: preview.endpoint ?? "", symbol: "shippingbox.and.arrow.backward")
            HStack(spacing: 14) {
                MetricCard(title: "Images", value: "\(preview.images)", symbol: "square.stack.3d.up.fill", subtitle: "Copied one at a time")
                MetricCard(title: "Containers", value: "\(preview.containers)", symbol: "shippingbox.fill",
                           subtitle: preview.running > 0 ? "\(preview.running) running, skipped: only stopped ones are copied" : "All stopped")
                MetricCard(title: "Volumes", value: "\(preview.volumes)", symbol: "externaldrive.fill",
                           subtitle: preview.volumeMigrationSupported ? "Named volume data" : "Volume copy needs Linux for now")
            }
            if let message = preview.message { Text(message).font(.callout).foregroundStyle(.secondary) }
            if preview.available {
                HStack(spacing: 10) {
                    Button { Task { await start("StartDockerImageMigration") } } label: { Label("Migrate images", systemImage: "square.stack.3d.up") }
                        .buttonStyle(PrimaryButtonStyle(color: accent))
                        .disabled(running || preview.images == 0)
                    Button { Task { await start("StartDockerContainerMigration") } } label: { Label("Migrate containers", systemImage: "shippingbox") }
                        .buttonStyle(SecondaryButtonStyle())
                        .disabled(running || preview.containers == 0)
                    Button { Task { await migrateVolumes() } } label: { Label("Migrate volumes", systemImage: "externaldrive") }
                        .buttonStyle(SecondaryButtonStyle())
                        .disabled(running || preview.volumes == 0 || !preview.volumeMigrationSupported)
                }
            }
        }
        .padding(20)
        .surface(cornerRadius: 20)
    }

    private func progressCard(_ status: MigrationStatus) -> some View {
        let what = status.kind == "container" ? "Containers" : status.kind == "volume" ? "Volumes" : "Images"
        let title = switch status.state {
        case "running": "Migrating \(what.lowercased())…"
        case "complete": "\(what) migrated"
        default: "\(what) migration failed"
        }
        let symbol = switch status.state {
        case "running": "arrow.triangle.2.circlepath"
        case "complete": "checkmark.circle.fill"
        default: "exclamationmark.triangle.fill"
        }
        return VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: title, detail: status.message, symbol: symbol)
            if status.isRunning {
                if let total = status.totalImages, total > 0 {
                    ProgressView(value: Double(min(status.importedImages ?? 0, total)), total: Double(total)) {
                        Text("\(status.importedImages ?? 0) of \(total)").font(.caption.monospacedDigit())
                    }
                } else {
                    ProgressView().progressViewStyle(.linear)
                }
                if let current = status.currentImage {
                    Text(current).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
            } else if let error = status.error {
                Text(error).font(.callout).foregroundStyle(.red).textSelection(.enabled)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(20)
        .surface(cornerRadius: 20)
        .tint(status.state == "failed" ? .red : accent)
    }

    // MARK: Actions

    private func scan(startSource: Bool = false) async {
        scanning = true
        let method = startSource ? "DockerService.StartDockerMigrationSource" : "DockerService.GetDockerMigrationPreview"
        if let next = await model.callOrReport(method, as: MigrationPreview.self) { preview = next }
        scanning = false
    }

    private func start(_ method: String) async {
        if let next = await model.callOrReport("DockerService.\(method)", as: MigrationStatus.self) { status = next }
    }

    private func migrateVolumes() async {
        guard let core = model.core else { return }
        status = MigrationStatus(kind: "volume", state: "running", message: "Copying named volumes…")
        do {
            let result = try await core.call("DockerService.MigrateDockerVolumes", as: DockerCLIResult.self)
            status = MigrationStatus(kind: "volume", state: "complete", message: result.output)
            await model.refreshResources()
        } catch {
            status = MigrationStatus(kind: "volume", state: "failed", message: "Docker volume migration failed.", error: error.localizedDescription)
        }
    }
}
