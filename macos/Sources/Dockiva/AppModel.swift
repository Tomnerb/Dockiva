import Foundation
import Observation

/// The app's state, refreshed from the core: on start, on Docker events, and
/// on a slow timer for container stats.
@MainActor @Observable
final class AppModel {
    enum Phase: Equatable { case starting, ready, failed(String) }

    var phase: Phase = .starting
    var section: SidebarItem? = .containers
    var status: DockerStatus?
    var engine: EngineStatus?
    var settings: AppSettings?
    var containers: [ContainerInfo] = []
    var images: [ImageInfo] = []
    var volumes: [VolumeInfo] = []
    var networks: [NetworkInfo] = []
    var stats: [String: ContainerResourceStats] = [:]
    var busy: Set<String> = []  // container IDs with an action in flight
    var lastError: String?
    var engineBusy = false

    private(set) var core: CoreClient?
    private var process: CoreProcess?
    private var streamHandlers: [String: (StreamOutputEvent) -> Void] = [:]
    private var earlyOutput: [String: [StreamOutputEvent]] = [:]  // before the stream's ID returned
    private var refreshQueued = false

    var runningCount: Int { containers.filter(\.isRunning).count }

    func start() async {
        guard core == nil else { return }
        do {
            let process = try CoreProcess()
            self.process = process
            let socket = process.socket
            let core = try await Task.detached { try CoreClient(socket: socket) }.value
            self.core = core
            phase = .ready
            Task { await self.consumeEvents(core) }
            Task { await self.pollStats() }
            await refreshAll()
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    func stop() { process?.stop() }

    // MARK: Refreshing

    func refreshAll() async {
        guard let core else { return }
        async let settings = try? core.call("AppService.GetSettings", as: AppSettings.self)
        async let status = try? core.call("DockerService.Status", as: DockerStatus.self)
        self.settings = await settings
        self.status = await status
        await refreshEngine()
        await refreshResources()
    }

    func refreshEngine() async {
        guard let core, let settings else { return }
        engine = try? await core.call("DockerService.GetEngineStatus", [settings.engineBackend, settings.wslDistro], as: EngineStatus.self)
    }

    func refreshResources() async {
        guard let core else { return }
        async let containers = try? core.call("DockerService.ListContainers", as: [ContainerInfo]?.self)
        async let images = try? core.call("DockerService.ListImages", as: [ImageInfo]?.self)
        async let volumes = try? core.call("DockerService.ListVolumes", as: [VolumeInfo]?.self)
        async let networks = try? core.call("DockerService.ListNetworks", as: [NetworkInfo]?.self)
        async let status = try? core.call("DockerService.Status", as: DockerStatus.self)
        if let c = await containers { self.containers = (c ?? []).sorted { ($0.composeProject ?? "", $0.name) < ($1.composeProject ?? "", $1.name) } }
        if let i = await images { self.images = i ?? [] }
        if let v = await volumes { self.volumes = v ?? [] }
        if let n = await networks { self.networks = n ?? [] }
        self.status = await status
    }

    /// Coalesces bursts of Docker events into one refresh.
    private func queueRefresh() {
        guard !refreshQueued else { return }
        refreshQueued = true
        Task {
            try? await Task.sleep(for: .milliseconds(250))
            refreshQueued = false
            await refreshResources()
        }
    }

    private func consumeEvents(_ core: CoreClient) async {
        for await event in core.events {
            switch event.name {
            case CoreEvents.dockerChanged:
                queueRefresh()
                if event.data.range(of: Data("\"engine\"".utf8)) != nil { await refreshEngine() }
            case CoreEvents.terminalOutput, CoreEvents.logOutput:
                if let output = try? JSONDecoder().decode(StreamOutputEvent.self, from: event.data) {
                    if let handler = streamHandlers[output.streamId] {
                        handler(output)
                    } else if earlyOutput.count < 32 {
                        earlyOutput[output.streamId, default: []].append(output)
                    }
                }
            default:
                break
            }
        }
        phase = .failed("The Dockiva core stopped. See ~/Library/Logs/Dockiva/core.log.")
    }

    private func pollStats() async {
        while !Task.isCancelled, let core {
            let running = containers.filter(\.isRunning).map(\.id)
            if !running.isEmpty, let list = try? await core.call("DockerService.GetContainerStats", [running], as: [ContainerResourceStats]?.self) {
                stats = Dictionary((list ?? []).map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            }
            try? await Task.sleep(for: .seconds(3))
        }
    }

    // MARK: Actions

    /// Runs an action, reporting its error, then refreshes.
    func perform(_ method: String, _ args: [any Encodable] = [], busyID: String? = nil) async {
        guard let core else { return }
        if let busyID { busy.insert(busyID) }
        defer { if let busyID { busy.remove(busyID) } }
        do {
            try await core.run(method, args)
        } catch {
            lastError = error.localizedDescription
        }
        await refreshResources()
    }

    func containerAction(_ action: String, _ container: ContainerInfo) async {
        switch action {
        case "remove": await perform("DockerService.RemoveContainer", [container.id, true], busyID: container.id)
        default: await perform("DockerService.\(action.capitalized)Container", [container.id], busyID: container.id)
        }
    }

    func engineAction(start: Bool) async {
        guard let core, let settings else { return }
        engineBusy = true
        defer { engineBusy = false }
        do {
            let result = try await core.call(start ? "DockerService.StartManagedEngine" : "DockerService.StopManagedEngine",
                                             [settings.engineBackend, settings.wslDistro], as: EngineActionResult.self)
            engine = result.status
        } catch {
            lastError = error.localizedDescription
        }
        await refreshAll()
    }

    func updateSettings(_ next: AppSettings) async {
        guard let core else { return }
        do {
            try await core.run("AppService.UpdateSettings", [next])
            settings = next
        } catch {
            lastError = error.localizedDescription
        }
    }

    func cliAction(_ method: String, _ args: [any Encodable]) async -> String? {
        guard let core else { return nil }
        do {
            let result = try await core.call(method, args, as: DockerCLIResult.self)
            await refreshResources()
            return result.output
        } catch {
            lastError = error.localizedDescription
            return nil
        }
    }

    // MARK: Streams (logs and terminals)

    func openStream(_ method: String, _ args: [any Encodable], handler: @escaping (StreamOutputEvent) -> Void) async throws -> String {
        guard let core else { throw CoreClient.CoreError(message: "The Dockiva core is not running") }
        let id = try await core.call(method, args, as: String.self)
        streamHandlers[id] = handler
        for output in earlyOutput.removeValue(forKey: id) ?? [] { handler(output) }
        return id
    }

    func closeStream(_ id: String, method: String) {
        streamHandlers[id] = nil
        earlyOutput[id] = nil
        guard let core else { return }
        Task { try? await core.run(method, [id]) }
    }
}
