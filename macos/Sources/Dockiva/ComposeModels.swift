import Foundation

// Mirrors of the Go core's Compose and inspect types (docker_extra_service.go,
// docker_platform.go). Go encodes nil slices as null, so lists are optional.

struct ComposeProjectDetails: Codable, Hashable {
    var project: String
    var workingDir: String
    var configFiles: [String]?
    var envFile: String?
}

struct ComposeImportResult: Codable, Hashable {
    var project: String
    var workingDir: String
    var configFiles: [String]?
    var output: String
}

struct ContainerDetails: Codable, Hashable {
    struct Mount: Codable, Hashable {
        var type: String
        var name: String?
        var source: String
        var destination: String
        var rw: Bool
    }

    struct Network: Codable, Hashable {
        var name: String
        var ipAddress: String
        var gateway: String
        var mac: String
    }

    var id: String
    var name: String
    var image: String  // the image ID; ContainerInfo.image has the reference
    var created: String
    var path: String
    var args: [String]?
    var entrypoint: [String]?
    var command: [String]?
    var workingDir: String
    var user: String
    var environment: [String]?
    var mounts: [Mount]?
    var networks: [Network]?
    var labels: [String: String]?
    var restartPolicy: String
    var state: String
    var exitCode: Int
    var startedAt: String
    var finishedAt: String

    /// "KEY=value" entries split at the first "=".
    var environmentPairs: [(key: String, value: String)] {
        (environment ?? []).map { entry in
            guard let eq = entry.firstIndex(of: "=") else { return (entry, "") }
            return (String(entry[..<eq]), String(entry[entry.index(after: eq)...]))
        }
    }

    /// A Go time (RFC 3339 with nanoseconds); nil for Go's zero time or an unparsable value.
    static func date(_ value: String) -> Date? {
        guard !value.isEmpty, !value.hasPrefix("0001-") else { return nil }
        let whole = value.replacingOccurrences(of: "\\.\\d+", with: "", options: .regularExpression)
        return ISO8601DateFormatter().date(from: whole)
    }

    /// An argv as one line, quoting arguments that contain spaces.
    static func commandLine(_ argv: [String]?) -> String {
        (argv ?? []).map { $0.contains(" ") || $0.isEmpty ? "\"\($0)\"" : $0 }.joined(separator: " ")
    }
}
