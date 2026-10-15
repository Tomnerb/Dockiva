import Foundation

// Mirrors of the Go core's JSON types (docker_service.go, engine_common.go,
// app_settings.go). Optional fields are the ones Go omits when empty.

struct PortInfo: Codable, Hashable {
    var privatePort: Int
    var publicPort: Int
    var type: String
    var display: String
    var url: String?
}

struct ContainerInfo: Codable, Identifiable, Hashable {
    var id: String
    var shortId: String
    var name: String
    var image: String
    var state: String
    var status: String
    var composeProject: String?
    var composeService: String?
    var ipAddress: String?
    var ports: [PortInfo]?

    var isRunning: Bool { state == "running" }
}

struct ContainerResourceStats: Codable, Hashable {
    var id: String
    var cpuPercent: Double
    var memoryUsage: UInt64
    var memoryLimit: UInt64
    var memoryPercent: Double
    var pids: UInt64
    var error: String?
}

struct ImageInfo: Codable, Identifiable, Hashable {
    var id: String
    var shortId: String
    var tags: [String]?
    var size: Int64
    var created: Int64
    var containers: Int64

    var title: String { tags?.first ?? shortId }
}

struct VolumeInfo: Codable, Identifiable, Hashable {
    var name: String
    var driver: String
    var scope: String
    var mountpoint: String
    var createdAt: String?
    var labels: [String: String]?

    var id: String { name }
}

struct NetworkInfo: Codable, Identifiable, Hashable {
    var id: String
    var shortId: String
    var name: String
    var driver: String
    var scope: String
    var `internal`: Bool
    var attachable: Bool
    var ingress: Bool
}

struct DockerStatus: Codable, Hashable {
    var connected: Bool
    var apiVersion: String
    var osType: String
    var error: String?
}

struct EngineStatus: Codable, Hashable {
    var backend: String
    var platform: String
    var supported: Bool
    var helperInstalled: Bool
    var engineInstalled: Bool
    var running: Bool
    var endpoint: String?
    var message: String?
}

struct EngineActionResult: Codable, Hashable {
    var status: EngineStatus
    var output: String?
}

struct AppSettings: Codable, Hashable {
    var settingsVersion: Int
    var engineBackend: String
    var runtimeProvider: String
    var dockerEndpoint: String
    var wslDistro: String
    var closeToTray: Bool
    var startAtLogin: Bool
    var startHidden: Bool
    var autoReconnectEngine: Bool
    var startEngineOnLaunch: Bool
}

struct DockerCLIResult: Codable, Hashable {
    var output: String
}

struct StreamOutputEvent: Codable, Hashable {
    var streamId: String
    var containerId: String
    var data: String?
    var error: String?
    var closed: Bool?
    var seq: UInt64
}

enum CoreEvents {
    static let dockerChanged = "dockiva:docker-event"
    static let terminalOutput = "dockiva:terminal-output"
    static let logOutput = "dockiva:log-output"
}

extension Int64 {
    var byteString: String { ByteCountFormatter.string(fromByteCount: self, countStyle: .file) }
}

extension UInt64 {
    var byteString: String { ByteCountFormatter.string(fromByteCount: Int64(clamping: self), countStyle: .memory) }
}
