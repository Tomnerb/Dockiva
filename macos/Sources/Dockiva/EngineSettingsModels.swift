import Foundation

// Mirrors of the Go core's engine, storage and migration types (runtime_common.go,
// docker_platform.go, docker_extra_service.go, host_container_storage.go).
// EngineStatus, EngineActionResult, AppSettings and DockerCLIResult are in Models.swift.
// Optional fields are the ones Go omits when empty.

/// RuntimeOverview: the active runtime and the ones that can be selected.
struct EngineRuntimeOverview: Codable, Hashable {
    var active: EngineRuntimeInfo
    var candidates: [EngineRuntimeCandidate]?
}

/// ContainerRuntimeInfo.
struct EngineRuntimeInfo: Codable, Hashable {
    var provider: String
    var displayName: String
    var endpoint: String?
    var connected: Bool
    var experimental: Bool
    var capabilities: [String: Bool]  // RuntimeCapabilities: containers, logs, compose, …
    var message: String?
}

/// RuntimeCandidateInfo.
struct EngineRuntimeCandidate: Codable, Hashable, Identifiable {
    var provider: String
    var displayName: String
    var available: Bool
    var detected: Bool
    var selectable: Bool
    var experimental: Bool
    var endpoint: String?
    var message: String?

    var id: String { provider }

    /// The engineBackend setting that selects this runtime (the Vue panel's selectRuntime).
    var backend: String {
        switch provider {
        case "vmfree": "vmfree"
        case "docker": "external"
        default: "vz"  // containerd in Dockiva's Linux VM, the native backend on macOS
        }
    }
}

/// PlatformInfo.
struct EnginePlatformInfo: Codable, Hashable {
    var os: String
    var arch: String
    var dockerCli: String?
    var dockerContext: String?
    var dockerHost: String?
    var hostSource: String?
}

/// DockerDiskUsageItem: one line of `docker system df`.
struct StorageDiskUsageItem: Codable, Hashable, Identifiable {
    var type: String  // Images, Containers, Local Volumes, Build Cache
    var totalCount: Int
    var active: Int
    var size: String  // as docker prints it, e.g. 6.489GB
    var reclaimable: String  // e.g. 1.196GB (18%)

    var id: String { type }
}

/// HostContainerStorageItem: an engine's data folder on this Mac.
struct StorageHostItem: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var description: String
    var path: String
    var sizeBytes: Int64
    var size: String
    var canReveal: Bool
    var canClean: Bool
    var engineRunning: Bool
    var cleanupLabel: String
}

/// DockerMigrationPreview: what the external Docker engine holds.
struct MigrationPreview: Codable, Hashable {
    var available: Bool
    var reachable: Bool
    var volumeMigrationSupported: Bool
    var endpoint: String?
    var containers: Int
    var running: Int
    var images: Int
    var volumes: Int
    var message: String?
}

/// DockerMigrationStatus: the tracked image or container migration job.
struct MigrationStatus: Codable, Hashable {
    var kind: String?  // nil for images, "container" or "volume"
    var state: String  // "" (idle), running, complete or failed
    var message: String
    var importedImages: Int?  // items done, whatever the kind
    var totalImages: Int?
    var currentImage: String?
    var error: String?

    var isRunning: Bool { state == "running" }
}

extension AppModel {
    /// Calls the core; a failure goes to lastError (the window's alert) and returns nil.
    func callOrReport<T: Decodable>(_ method: String, _ args: [any Encodable] = [], as type: T.Type = T.self) async -> T? {
        guard let core else { return nil }
        do {
            return try await core.call(method, args, as: T.self)
        } catch {
            lastError = error.localizedDescription
            return nil
        }
    }
}
