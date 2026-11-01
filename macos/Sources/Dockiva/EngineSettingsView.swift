import SwiftUI

/// The engine screen: the current engine and its controls, the backend choice,
/// the external Docker endpoint and startup behavior (the Vue EngineSettingsPanel).
@MainActor  // its async helpers read the model; only body is main-actor in the macOS 14 SDK
struct EngineSettingsView: View {
    @Environment(AppModel.self) private var model
    @State private var overview: EngineRuntimeOverview?
    @State private var platform: EnginePlatformInfo?
    @State private var endpoint = ""
    @State private var busy: String?  // the backend being selected, "discover" or "delete"
    @State private var confirmDelete = false

    private let accent = SidebarItem.engine.accent
    private var engine: EngineStatus? { model.engine }
    private var backend: String { model.settings?.engineBackend ?? "" }
    private var running: Bool { engine?.running == true }
    private var working: Bool { model.engineBusy || busy != nil }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                FeatureHeader(title: "Engine", subtitle: engine?.message ?? "The Linux container engine behind Dockiva",
                              symbol: SidebarItem.engine.symbol, accent: accent) { actions }
                metrics
                backendCard
                if backend == "external" { endpointCard }
                if let endpoint = engine?.endpoint, !endpoint.isEmpty { terminalCard(endpoint) }
                card(title: "Startup", detail: "How Dockiva treats the engine when it opens", symbol: "gearshape") { SettingsToggles() }
            }
            .padding(28)
        }
        .task { await reload() }
        .confirmationDialog(backend == "vmfree" ? "Delete Dockiva Engine's data?" : "Delete the Dockiva Linux VM?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button(backend == "vmfree" ? "Delete Data" : "Delete VM", role: .destructive) { Task { await deleteEngine() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(backend == "vmfree"
                 ? "This deletes every container, image and volume in Dockiva Engine (~/.dockiva-engine). It starts empty next time. Other Docker engines are not touched."
                 : "This deletes the VM's disk with every container, image and volume stored in it. Other Docker engines are not touched.")
        }
    }

    // MARK: Current engine

    private var actions: some View {
        HStack(spacing: 8) {
            if working { ProgressView().controlSize(.small) }
            Button { Task { await reload() } } label: { Image(systemName: "arrow.clockwise") }
                .buttonStyle(SecondaryButtonStyle()).help("Refresh")
            Group {
                if !running {
                    Button { engineAction(start: true) } label: {
                        Label(backend == "external" ? "Reconnect" : "Start engine", systemImage: "power")
                    }
                    .buttonStyle(PrimaryButtonStyle(color: accent))
                } else if backend == "vz" || backend == "vmfree" {  // an external Docker is not Dockiva's to stop
                    Button { engineAction(start: false) } label: { Label("Stop engine", systemImage: "stop.fill") }
                        .buttonStyle(SecondaryButtonStyle())
                }
                if backend == "vmfree" || backend == "vz" && engine?.engineInstalled == true {
                    Button { confirmDelete = true } label: { Image(systemName: "trash") }
                        .buttonStyle(SecondaryButtonStyle(destructive: true))
                        .disabled(running)
                        .help(running ? "Stop the engine before deleting it" : "Delete the engine and its data")
                }
            }
            .disabled(working || engine?.supported == false)
        }
    }

    private var metrics: some View {
        HStack(spacing: 14) {
            MetricCard(title: "State", value: running ? "Running" : "Stopped", symbol: running ? "checkmark.circle.fill" : "pause.circle",
                       subtitle: model.status?.connected == true ? "Docker API reachable" : "Docker API not reachable")
            MetricCard(title: "Backend", value: overview?.candidates?.first { $0.backend == backend }?.displayName ?? backend,
                       symbol: "bolt.horizontal.circle", subtitle: engine?.endpoint ?? "No endpoint")
            MetricCard(title: "API", value: model.status?.apiVersion.isEmpty == false ? model.status!.apiVersion : "—",
                       symbol: "chevron.left.forwardslash.chevron.right", subtitle: "Docker Engine API version")
        }
    }

    // MARK: Backend

    private var backendCard: some View {
        card(title: "Backend", detail: "Where Dockiva runs your containers", symbol: "square.stack.3d.down.right") {
            if let overview {
                ForEach(overview.candidates ?? []) { candidateRow($0) }
                let capabilities = overview.active.capabilities.filter(\.value).keys.sorted()
                if !capabilities.isEmpty {
                    Text("\(overview.active.displayName) supports: \(capabilities.joined(separator: " · "))")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } else {
                ProgressView()
            }
        }
    }

    private func candidateRow(_ candidate: EngineRuntimeCandidate) -> some View {
        let selected = candidate.backend == backend
        return HStack(alignment: .top, spacing: 12) {
            Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(selected ? accent : .secondary)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(candidate.displayName).font(.system(size: 14, weight: .semibold, design: .rounded))
                    if candidate.experimental { badge("experimental", .orange) }
                }
                if let message = candidate.message { Text(message).font(.caption).foregroundStyle(.secondary) }
                if let endpoint = candidate.endpoint {
                    Text(endpoint).font(.caption2.monospaced()).foregroundStyle(.tertiary).lineLimit(1).truncationMode(.middle)
                }
            }
            Spacer(minLength: 12)
            if selected {
                badge(overview?.active.connected == true ? "active" : "selected", DockivaPalette.running)
            } else if candidate.selectable {
                Button(busy == candidate.backend ? "Switching…" : "Use") { Task { await select(candidate.backend) } }
                    .buttonStyle(SecondaryButtonStyle())
                    .disabled(working)
            } else {
                badge(candidate.detected ? "detected" : "not detected", .secondary)
            }
        }
        .padding(12)
        .insetSurface(cornerRadius: 12)
    }

    // MARK: External Docker

    private var endpointCard: some View {
        card(title: "External Docker", detail: "Docker Desktop, OrbStack, Colima or any Docker API endpoint",
             symbol: "point.3.connected.trianglepath.dotted") {
            HStack(spacing: 10) {
                TextField("unix:///var/run/docker.sock or tcp://host:2375", text: $endpoint)
                    .textFieldStyle(.plain)
                    .font(.system(.callout, design: .monospaced))
                    .padding(.horizontal, 11)
                    .padding(.vertical, 8)
                    .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .onSubmit(applyEndpoint)
                Button { discover() } label: { Label(busy == "discover" ? "Looking…" : "Discover", systemImage: "dot.radiowaves.left.and.right") }
                    .buttonStyle(SecondaryButtonStyle())
                    .help("Find a running Docker engine on this Mac")
                Button(action: applyEndpoint) { Label(busy == "external" ? "Applying…" : "Apply", systemImage: "checkmark") }
                    .buttonStyle(PrimaryButtonStyle(color: accent))
                    .disabled(endpoint.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .disabled(working)
            Text("If a saved local socket no longer exists, Dockiva uses the active Docker CLI context.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func terminalCard(_ endpoint: String) -> some View {
        card(title: "Use it from a terminal", detail: "The docker CLI and Compose talk to this engine", symbol: "terminal") {
            HStack {
                Text("export DOCKER_HOST=\(endpoint)")
                    .font(.system(.callout, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button { copy("export DOCKER_HOST=\(endpoint)") } label: { Label("Copy", systemImage: "doc.on.doc") }
                    .buttonStyle(SecondaryButtonStyle())
            }
            .padding(12)
            .background(.black.opacity(0.3), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            if let platform {
                Text(platform.dockerCli.map { "docker CLI: \($0)" } ?? "No docker CLI found. Storage cleanup and migration need it.")
                    .font(.caption).foregroundStyle(platform.dockerCli == nil ? Color.orange : .secondary)
            }
        }
    }

    // MARK: Pieces

    private func card<Content: View>(title: String, detail: String, symbol: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionTitle(title: title, detail: detail, symbol: symbol)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(20)
        .surface(cornerRadius: 20)
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.bold)).padding(.horizontal, 7).padding(.vertical, 3)
            .background(color.opacity(0.18), in: Capsule())
    }

    // MARK: Actions

    private func reload() async {
        await model.refreshAll()
        if endpoint.isEmpty { endpoint = model.settings?.dockerEndpoint ?? "" }
        guard let core = model.core else { return }
        async let overview = try? core.call("DockerService.GetRuntimeOverview", as: EngineRuntimeOverview.self)
        async let platform = try? core.call("DockerService.GetPlatformInfo", as: EnginePlatformInfo.self)
        self.overview = await overview
        self.platform = await platform
    }

    private func engineAction(start: Bool) {
        Task {
            await model.engineAction(start: start)
            await reload()
        }
    }

    /// Switches the engine and saves it in settings, as the Vue panel's chooseBackend.
    private func select(_ next: String, endpoint externalEndpoint: String? = nil) async {
        guard var settings = model.settings else { return }
        busy = next
        defer { busy = nil }
        let provider = await model.callOrReport("DockerService.SelectEngineBackend",
                                                [next, settings.wslDistro, externalEndpoint ?? settings.dockerEndpoint], as: String.self)
        if let provider {
            settings.engineBackend = next
            settings.runtimeProvider = provider
            // The core may resolve a missing socket to the active context: save what it uses.
            if next == "external", let identity = await model.callOrReport("DockerService.DockerEndpointIdentity", as: [String: String].self),
               let saved = identity["endpoint"], !saved.isEmpty {
                settings.dockerEndpoint = saved
                endpoint = saved
            }
            await model.updateSettings(settings)
        }
        await reload()
    }

    private func applyEndpoint() {
        let value = endpoint.trimmingCharacters(in: .whitespaces)
        guard !value.isEmpty else { return }
        Task { await select("external", endpoint: value) }
    }

    private func discover() {
        Task {
            busy = "discover"
            if let found = await model.callOrReport("DockerService.DiscoverDockerEndpoint", as: String.self) { endpoint = found }
            busy = nil
        }
    }

    private func deleteEngine() async {
        guard let settings = model.settings else { return }
        busy = "delete"
        if let result = await model.callOrReport("DockerService.DeleteManagedEngine", [settings.engineBackend, settings.wslDistro],
                                                 as: EngineActionResult.self) {
            model.engine = result.status
        }
        busy = nil
        await reload()
    }
}
