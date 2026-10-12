import Foundation
import Virtualization
import Darwin

private let defaultGuestPort: UInt32 = 10250
private let defaultDockerGuestPort: UInt32 = 10251

struct StatusDocument: Codable {
    var running: Bool
    var pid: Int32
    var kernel: String
    var disk: String
    var proxySocket: String
    var cpuCount: Int
    var memoryMiB: Int
    var startedAt: String
}

struct Arguments {
    let command: String
    let kernel: String?
    let disk: String?
    let stateDir: String
    let cpuCount: Int
    let memoryMiB: Int
    let guestPort: UInt32
    let dockerGuestPort: UInt32

    static func parse() throws -> Arguments {
        let values = Array(CommandLine.arguments.dropFirst())

        guard let command = values.first else {
            throw VMMError.usage
        }

        func value(_ name: String) -> String? {
            guard let index = values.firstIndex(of: name),
                  index + 1 < values.count else {
                return nil
            }
            return values[index + 1]
        }

        let home = FileManager.default.homeDirectoryForCurrentUser
        let defaultState = home
            .appendingPathComponent("Library")
            .appendingPathComponent("Application Support")
            .appendingPathComponent("Dockiva")
            .appendingPathComponent("run")
            .path

        return Arguments(
            command: command,
            kernel: value("--kernel"),
            disk: value("--disk"),
            stateDir: value("--state-dir") ?? defaultState,
            cpuCount: Int(value("--cpu") ?? "4") ?? 4,
            memoryMiB: Int(value("--memory-mib") ?? "2048") ?? 2048,
            guestPort: UInt32(value("--guest-port") ?? "\(defaultGuestPort)") ?? defaultGuestPort,
            dockerGuestPort: UInt32(value("--docker-guest-port") ?? "\(defaultDockerGuestPort)") ?? defaultDockerGuestPort
        )
    }
}

enum VMMError: Error, CustomStringConvertible {
    case usage
    case missing(String)
    case invalid(String)
    case system(String)

    var description: String {
        switch self {
        case .usage:
            return """
            usage:
              dockiva-vmm serve --kernel PATH --disk PATH --state-dir PATH [--cpu 4] [--memory-mib 2048] [--guest-port 10250]
              dockiva-vmm status --state-dir PATH
              dockiva-vmm stop --state-dir PATH
            """
        case .missing(let value):
            return "missing: \(value)"
        case .invalid(let value):
            return "invalid: \(value)"
        case .system(let value):
            return value
        }
    }
}

final class VMStopDelegate: NSObject, VZVirtualMachineDelegate {
    var onStop: (() -> Void)?

    func guestDidStop(_ virtualMachine: VZVirtualMachine) {
        onStop?()
    }

    func virtualMachine(
        _ virtualMachine: VZVirtualMachine,
        didStopWithError error: any Error
    ) {
        fputs("dockiva-vmm: VM stopped with error: \(error)\n", stderr)
        onStop?()
    }
}

/// Forwards the guest console to stderr and reports a kernel panic: a panicked
/// guest spins on its vCPUs, and a reboot (panic=N) only boots it again.
final class ConsoleWatcher: @unchecked Sendable {
    private let pipe = Pipe()
    private let lock = NSLock()
    private var tail = Data()
    private var reported = false
    var onPanic: (() -> Void)?

    var writeHandle: FileHandle { pipe.fileHandleForWriting }

    init() {
        let marker = Data("Kernel panic - not syncing".utf8)
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard let self, !data.isEmpty else { return }
            FileHandle.standardError.write(data)
            self.lock.lock()
            self.tail.append(data)
            let found = !self.reported && self.tail.range(of: marker) != nil
            if found { self.reported = true }
            self.tail = self.tail.suffix(marker.count)       // a marker split across reads
            self.lock.unlock()
            if found {
                fputs("\ndockiva-vmm: guest kernel panicked; stopping the VM\n", stderr)
                self.onPanic?()
            }
        }
    }
}

final class Relay: @unchecked Sendable {
    private let lock = NSLock()
    private var closed = false

    let localFD: Int32
    let guestConnection: VZVirtioSocketConnection

    init(localFD: Int32, guestConnection: VZVirtioSocketConnection) {
        self.localFD = localFD
        self.guestConnection = guestConnection
    }

    func start() {
        let guestFD = guestConnection.fileDescriptor

        DispatchQueue.global(qos: .userInitiated).async {
            self.copy(from: self.localFD, to: guestFD)
        }

        DispatchQueue.global(qos: .userInitiated).async {
            self.copy(from: guestFD, to: self.localFD)
        }
    }

    private func copy(from source: Int32, to target: Int32) {
        guard source >= 0, target >= 0 else {
            closeBoth()
            return
        }

        var buffer = [UInt8](repeating: 0, count: 64 * 1024)

        while true {
            let count = Darwin.read(source, &buffer, buffer.count)

            if count <= 0 {
                break
            }

            var offset = 0

            while offset < count {
                let written = buffer.withUnsafeBytes { raw -> Int in
                    guard let base = raw.baseAddress else {
                        return -1
                    }

                    return Darwin.write(
                        target,
                        base.advanced(by: offset),
                        count - offset
                    )
                }

                if written <= 0 {
                    closeBoth()
                    return
                }

                offset += written
            }
        }

        closeBoth()
    }

    private func closeBoth() {
        lock.lock()
        defer { lock.unlock() }

        guard !closed else {
            return
        }

        closed = true
        Darwin.shutdown(localFD, SHUT_RDWR)
        Darwin.close(localFD)
        guestConnection.close()
    }
}

final class UnixProxyServer {
    private let path: String
    private let socketDevice: VZVirtioSocketDevice
    private let guestPort: UInt32

    private var serverFD: Int32 = -1
    private var running = false

    init(
        path: String,
        socketDevice: VZVirtioSocketDevice,
        guestPort: UInt32
    ) {
        self.path = path
        self.socketDevice = socketDevice
        self.guestPort = guestPort
    }

    func start() throws {
        let directory = URL(fileURLWithPath: path).deletingLastPathComponent()

        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true
        )

        try? FileManager.default.removeItem(atPath: path)

        serverFD = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard serverFD >= 0 else {
            throw VMMError.system("socket(AF_UNIX) failed: \(String(cString: strerror(errno)))")
        }

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)

        let maxPath = MemoryLayout.size(ofValue: address.sun_path)
        let utf8 = Array(path.utf8)

        guard utf8.count < maxPath else {
            Darwin.close(serverFD)
            throw VMMError.invalid("Unix socket path is too long")
        }

        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(
                to: CChar.self,
                capacity: maxPath
            ) { chars in
                memset(chars, 0, maxPath)

                for index in 0..<utf8.count {
                    chars[index] = CChar(bitPattern: utf8[index])
                }
            }
        }

        let bindResult = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(
                to: sockaddr.self,
                capacity: 1
            ) { sockaddrPointer in
                Darwin.bind(
                    serverFD,
                    sockaddrPointer,
                    socklen_t(MemoryLayout<sockaddr_un>.size)
                )
            }
        }

        guard bindResult == 0 else {
            let message = String(cString: strerror(errno))
            Darwin.close(serverFD)
            serverFD = -1
            throw VMMError.system("bind(\(path)) failed: \(message)")
        }

        chmod(path, 0o600)

        guard Darwin.listen(serverFD, 64) == 0 else {
            let message = String(cString: strerror(errno))
            Darwin.close(serverFD)
            serverFD = -1
            throw VMMError.system("listen failed: \(message)")
        }

        running = true

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            self?.acceptLoop()
        }
    }

    func stop() {
        running = false

        if serverFD >= 0 {
            Darwin.shutdown(serverFD, SHUT_RDWR)
            Darwin.close(serverFD)
            serverFD = -1
        }

        try? FileManager.default.removeItem(atPath: path)
    }

    private func acceptLoop() {
        while running {
            let clientFD = Darwin.accept(serverFD, nil, nil)

            if clientFD < 0 {
                if running {
                    usleep(50_000)
                }
                continue
            }

            DispatchQueue.main.async { [socketDevice, guestPort] in
                socketDevice.connect(toPort: guestPort) { result in
                    switch result {
                    case .success(let connection):
                        Relay(
                            localFD: clientFD,
                            guestConnection: connection
                        ).start()

                    case .failure(let error):
                        fputs(
                            "dockiva-vmm: vsock connect failed: \(error)\n",
                            stderr
                        )
                        Darwin.close(clientFD)
                    }
                }
            }
        }
    }
}

@MainActor
final class VMRuntime {
    let args: Arguments
    let vm: VZVirtualMachine
    let delegate: VMStopDelegate

    private var proxy: UnixProxyServer?
    private var dockerProxy: UnixProxyServer?
    let console = ConsoleWatcher()

    init(args: Arguments) throws {
        self.args = args

        guard VZVirtualMachine.isSupported else {
            throw VMMError.system(
                "Virtualization.framework is not supported on this Mac"
            )
        }

        guard let kernel = args.kernel else {
            throw VMMError.missing("--kernel")
        }

        guard let disk = args.disk else {
            throw VMMError.missing("--disk")
        }

        let kernelURL = URL(fileURLWithPath: kernel)
        let diskURL = URL(fileURLWithPath: disk)

        guard FileManager.default.fileExists(atPath: kernel) else {
            throw VMMError.missing(kernel)
        }

        guard FileManager.default.fileExists(atPath: disk) else {
            throw VMMError.missing(disk)
        }

        let configuration = VZVirtualMachineConfiguration()

        configuration.cpuCount = max(
            VZVirtualMachineConfiguration.minimumAllowedCPUCount,
            min(
                args.cpuCount,
                VZVirtualMachineConfiguration.maximumAllowedCPUCount
            )
        )

        let requestedMemory = UInt64(max(args.memoryMiB, 512)) * 1024 * 1024

        configuration.memorySize = max(
            VZVirtualMachineConfiguration.minimumAllowedMemorySize,
            min(
                requestedMemory,
                VZVirtualMachineConfiguration.maximumAllowedMemorySize
            )
        )

        let bootLoader = VZLinuxBootLoader(kernelURL: kernelURL)
        bootLoader.commandLine =
            "console=hvc0 root=/dev/vda rw init=/sbin/dockiva-init"
        configuration.bootLoader = bootLoader

        let diskAttachment = try VZDiskImageStorageDeviceAttachment(
            url: diskURL,
            readOnly: false,
            cachingMode: .automatic,
            synchronizationMode: .full
        )

        configuration.storageDevices = [
            VZVirtioBlockDeviceConfiguration(
                attachment: diskAttachment
            )
        ]

        let networkDevice = VZVirtioNetworkDeviceConfiguration()
        networkDevice.attachment = VZNATNetworkDeviceAttachment()
        configuration.networkDevices = [networkDevice]

        configuration.entropyDevices = [
            VZVirtioEntropyDeviceConfiguration()
        ]

        configuration.socketDevices = [
            VZVirtioSocketDeviceConfiguration()
        ]

        let projectsURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Projects")

        if FileManager.default.fileExists(atPath: projectsURL.path) {
            let sharedDirectory = VZSharedDirectory(
                url: projectsURL,
                readOnly: false
            )
            let share = VZSingleDirectoryShare(
                directory: sharedDirectory
            )
            let fileSystem = VZVirtioFileSystemDeviceConfiguration(
                tag: "dockiva"
            )
            fileSystem.share = share
            configuration.directorySharingDevices = [fileSystem]
        }

        let serialPort = VZVirtioConsoleDeviceSerialPortConfiguration()
        serialPort.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: nil,
            fileHandleForWriting: console.writeHandle
        )
        configuration.serialPorts = [serialPort]

        try configuration.validate()

        self.vm = VZVirtualMachine(configuration: configuration)
        self.delegate = VMStopDelegate()
        self.vm.delegate = delegate
    }

    func start() async throws {
        try await vm.start()

        guard let socketDevice =
                vm.socketDevices.first as? VZVirtioSocketDevice else {
            throw VMMError.system(
                "Virtualization.framework did not expose the virtio socket device"
            )
        }

        let proxyPath = URL(fileURLWithPath: args.stateDir)
            .appendingPathComponent("containerd.sock")
            .path

        let proxy = UnixProxyServer(
            path: proxyPath,
            socketDevice: socketDevice,
            guestPort: args.guestPort
        )

        try proxy.start()
        self.proxy = proxy

        let dockerProxyPath = URL(fileURLWithPath: args.stateDir)
            .appendingPathComponent("docker.sock")
            .path
        let dockerProxy = UnixProxyServer(
            path: dockerProxyPath,
            socketDevice: socketDevice,
            guestPort: args.dockerGuestPort
        )
        try dockerProxy.start()
        self.dockerProxy = dockerProxy

        try writeStatus(running: true)
    }

    func stop() async {
        proxy?.stop()
        proxy = nil
        dockerProxy?.stop()
        dockerProxy = nil

        if vm.canRequestStop {
            try? vm.requestStop()
            try? await Task.sleep(
                nanoseconds: 1_500_000_000
            )
        }

        if vm.canStop {
            try? await vm.stop()
        }

        try? writeStatus(running: false)
    }

    func writeStatus(running: Bool) throws {
        try FileManager.default.createDirectory(
            atPath: args.stateDir,
            withIntermediateDirectories: true
        )

        let kernel = args.kernel ?? ""
        let disk = args.disk ?? ""

        let document = StatusDocument(
            running: running,
            pid: getpid(),
            kernel: kernel,
            disk: disk,
            proxySocket: URL(fileURLWithPath: args.stateDir)
                .appendingPathComponent("containerd.sock")
                .path,
            cpuCount: args.cpuCount,
            memoryMiB: args.memoryMiB,
            startedAt: ISO8601DateFormatter().string(from: Date())
        )

        let data = try JSONEncoder().encode(document)

        try data.write(
            to: URL(fileURLWithPath: args.stateDir)
                .appendingPathComponent("status.json"),
            options: .atomic
        )

        try "\(getpid())".write(
            to: URL(fileURLWithPath: args.stateDir)
                .appendingPathComponent("vmm.pid"),
            atomically: true,
            encoding: .utf8
        )
    }
}

func readPID(stateDir: String) -> Int32? {
    let path = URL(fileURLWithPath: stateDir)
        .appendingPathComponent("vmm.pid")
        .path

    guard let text = try? String(
        contentsOfFile: path,
        encoding: .utf8
    ) else {
        return nil
    }

    return Int32(text.trimmingCharacters(in: .whitespacesAndNewlines))
}

func processIsAlive(_ pid: Int32) -> Bool {
    guard pid > 0 else {
        return false
    }

    if Darwin.kill(pid, 0) == 0 {
        return true
    }

    return errno == EPERM
}

func printStatus(stateDir: String) {
    let statusPath = URL(fileURLWithPath: stateDir)
        .appendingPathComponent("status.json")
        .path

    let pid = readPID(stateDir: stateDir)
    let alive = pid.map(processIsAlive) ?? false

    if alive,
       let data = FileManager.default.contents(atPath: statusPath),
       var document = try? JSONDecoder().decode(
           StatusDocument.self,
           from: data
       ) {
        document.running = true

        if let output = try? JSONEncoder().encode(document),
           let text = String(data: output, encoding: .utf8) {
            print(text)
            return
        }
    }

    let fallback = StatusDocument(
        running: false,
        pid: pid ?? 0,
        kernel: "",
        disk: "",
        proxySocket: URL(fileURLWithPath: stateDir)
            .appendingPathComponent("containerd.sock")
            .path,
        cpuCount: 0,
        memoryMiB: 0,
        startedAt: ""
    )

    if let output = try? JSONEncoder().encode(fallback),
       let text = String(data: output, encoding: .utf8) {
        print(text)
    }
}

func stopDaemon(stateDir: String) throws {
    guard let pid = readPID(stateDir: stateDir) else {
        print("Dockiva native VM is not running.")
        return
    }

    guard processIsAlive(pid) else {
        print("Dockiva native VM is not running.")
        return
    }

    guard Darwin.kill(pid, SIGTERM) == 0 else {
        throw VMMError.system(
            "failed to signal VMM process \(pid): \(String(cString: strerror(errno)))"
        )
    }

    for _ in 0..<50 {
        if !processIsAlive(pid) {
            print("Dockiva native VM stopped.")
            return
        }

        usleep(100_000)
    }

    throw VMMError.system(
        "VMM process did not stop after SIGTERM"
    )
}

@MainActor
func serve(args: Arguments) async throws {
    let runtime = try VMRuntime(args: args)

    let semaphore = DispatchSemaphore(value: 0)

    runtime.delegate.onStop = {
        semaphore.signal()
    }
    runtime.console.onPanic = {
        semaphore.signal()
    }

    signal(SIGTERM, SIG_IGN)
    signal(SIGINT, SIG_IGN)

    let termSource = DispatchSource.makeSignalSource(
        signal: SIGTERM,
        queue: .main
    )

    let intSource = DispatchSource.makeSignalSource(
        signal: SIGINT,
        queue: .main
    )

    termSource.setEventHandler {
        semaphore.signal()
    }

    intSource.setEventHandler {
        semaphore.signal()
    }

    termSource.resume()
    intSource.resume()

    try await runtime.start()

    await withCheckedContinuation { continuation in
        DispatchQueue.global(qos: .utility).async {
            semaphore.wait()
            continuation.resume()
        }
    }

    await runtime.stop()
}

@main
struct DockivaVMMMain {
    static func main() async {
        do {
            let args = try Arguments.parse()

            switch args.command {
            case "serve":
                try await serve(args: args)

            case "status":
                printStatus(stateDir: args.stateDir)

            case "stop":
                try stopDaemon(stateDir: args.stateDir)

            default:
                throw VMMError.usage
            }
        } catch {
            fputs("dockiva-vmm: \(error)\n", stderr)
            exit(1)
        }
    }
}
