import Foundation

/// The Go core's API (core_api.go): one JSON object per line over a unix socket.
/// Calls are matched to responses by id; events arrive on the same connection.
final class CoreClient: @unchecked Sendable {
    struct CoreError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    struct Event: Sendable {
        let name: String
        let data: Data  // the event's JSON payload
    }

    let events: AsyncStream<Event>
    private let eventSink: AsyncStream<Event>.Continuation
    private let fd: Int32
    private let lock = NSLock()
    private var nextID: UInt64 = 0
    private var pending: [UInt64: CheckedContinuation<Data, Error>] = [:]
    private var closed = false

    /// Connects to a listening core, retrying while it starts.
    init(socket path: String, timeout: TimeInterval = 15) throws {
        var connected: Int32 = -1
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            connected = CoreClient.connect(path)
            if connected >= 0 { break }
            Thread.sleep(forTimeInterval: 0.05)
        } while Date() < deadline
        guard connected >= 0 else { throw CoreError(message: "Could not connect to the Dockiva core at \(path)") }
        fd = connected
        (events, eventSink) = AsyncStream.makeStream(of: Event.self, bufferingPolicy: .bufferingNewest(512))
        let reader = Thread { [weak self] in self?.readLoop() }
        reader.name = "dockiva-core-reader"
        reader.start()
    }

    deinit { Darwin.close(fd) }

    private static func connect(_ path: String) -> Int32 {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return -1 }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: address.sun_path) else { Darwin.close(fd); return -1 }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
            raw[bytes.count] = 0
        }
        let ok = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if ok != 0 { Darwin.close(fd); return -1 }
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        return fd
    }

    /// Calls "Service.Method" and decodes its result.
    func call<T: Decodable>(_ method: String, _ args: [any Encodable] = [], as type: T.Type = T.self) async throws -> T {
        let raw = try await callRaw(method, args)
        return try JSONDecoder().decode(T.self, from: raw)
    }

    /// Calls a method whose result is ignored (or that returns only an error).
    func run(_ method: String, _ args: [any Encodable] = []) async throws {
        _ = try await callRaw(method, args)
    }

    private func callRaw(_ method: String, _ args: [any Encodable]) async throws -> Data {
        let encodedArgs = try args.map { try JSONEncoder().encode(AnyEncodable($0)) }
        return try await withCheckedThrowingContinuation { continuation in
            lock.lock()
            if closed {
                lock.unlock()
                continuation.resume(throwing: CoreError(message: "The Dockiva core is not running"))
                return
            }
            nextID += 1
            let id = nextID
            pending[id] = continuation
            lock.unlock()
            var line = Data("{\"id\":\(id),\"method\":".utf8)
            line.append(try! JSONEncoder().encode(method))
            line.append(Data(",\"args\":[".utf8))
            for (i, arg) in encodedArgs.enumerated() {
                if i > 0 { line.append(Data(",".utf8)) }
                line.append(arg)
            }
            line.append(Data("]}\n".utf8))
            if !send(line) { finish(id, .failure(CoreError(message: "The Dockiva core connection closed"))) }
        }
    }

    private func send(_ data: Data) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return data.withUnsafeBytes { raw -> Bool in
            var offset = 0
            while offset < raw.count {
                let n = Darwin.write(fd, raw.baseAddress! + offset, raw.count - offset)
                if n < 0 && errno == EINTR { continue }
                if n <= 0 { return false }
                offset += n
            }
            return true
        }
    }

    private func finish(_ id: UInt64, _ result: Result<Data, Error>) {
        lock.lock()
        let continuation = pending.removeValue(forKey: id)
        lock.unlock()
        continuation?.resume(with: result)
    }

    private func readLoop() {
        var buffer = Data()
        var chunk = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let n = Darwin.read(fd, &chunk, chunk.count)
            if n < 0 && errno == EINTR { continue }
            if n <= 0 { break }
            buffer.append(contentsOf: chunk[0..<n])
            while let newline = buffer.firstIndex(of: 0x0A) {
                let line = buffer[buffer.startIndex..<newline]
                buffer.removeSubrange(buffer.startIndex...newline)
                handle(Data(line))
            }
        }
        lock.lock()
        closed = true
        let waiting = pending
        pending.removeAll()
        lock.unlock()
        for continuation in waiting.values {
            continuation.resume(throwing: CoreError(message: "The Dockiva core connection closed"))
        }
        eventSink.finish()
    }

    private func handle(_ line: Data) {
        guard let object = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { return }
        if let name = object["event"] as? String {
            let data = (try? JSONSerialization.data(withJSONObject: object["data"] ?? NSNull(), options: .fragmentsAllowed)) ?? Data("null".utf8)
            eventSink.yield(Event(name: name, data: data))
            return
        }
        guard let id = (object["id"] as? NSNumber)?.uint64Value else { return }
        if let message = object["error"] as? String {
            finish(id, .failure(CoreError(message: message)))
        } else {
            let result = (try? JSONSerialization.data(withJSONObject: object["result"] ?? NSNull(), options: .fragmentsAllowed)) ?? Data("null".utf8)
            finish(id, .success(result))
        }
    }
}

/// Decodes a result that is absent or null (methods returning only an error).
struct Empty: Decodable {}

private struct AnyEncodable: Encodable {
    let value: any Encodable
    init(_ value: any Encodable) { self.value = value }
    func encode(to encoder: Encoder) throws { try value.encode(to: encoder) }
}

/// Starts `dockiva-core --core SOCKET` and keeps a pipe on its standard input:
/// when this app exits, for any reason, the pipe closes and the core shuts down.
final class CoreProcess {
    let socket: String
    private let process = Process()
    private let lifeline = Pipe()

    init() throws {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Dockiva", isDirectory: true)
        try FileManager.default.createDirectory(at: support, withIntermediateDirectories: true)
        socket = support.appendingPathComponent("core-\(getpid()).sock").path

        let logs = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs/Dockiva", isDirectory: true)
        try FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        let logURL = logs.appendingPathComponent("core.log")
        if !FileManager.default.fileExists(atPath: logURL.path) {
            FileManager.default.createFile(atPath: logURL.path, contents: nil)
        }
        let log = try FileHandle(forWritingTo: logURL)
        try log.truncate(atOffset: 0)

        process.executableURL = try CoreProcess.executable()
        process.arguments = ["--core", socket]
        process.standardInput = lifeline
        process.standardOutput = log
        process.standardError = log
        try process.run()
    }

    var isRunning: Bool { process.isRunning }

    func stop() {
        try? lifeline.fileHandleForWriting.close()
    }

    /// DOCKIVA_CORE for development, else dockiva-core beside this executable.
    static func executable() throws -> URL {
        if let override = ProcessInfo.processInfo.environment["DOCKIVA_CORE"], !override.isEmpty {
            return URL(fileURLWithPath: override)
        }
        let beside = Bundle.main.executableURL!.deletingLastPathComponent().appendingPathComponent("dockiva-core")
        guard FileManager.default.isExecutableFile(atPath: beside.path) else {
            throw CoreClient.CoreError(message: "dockiva-core is missing from the app bundle")
        }
        return beside
    }
}
