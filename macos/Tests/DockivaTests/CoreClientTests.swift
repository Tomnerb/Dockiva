import Foundation
import XCTest
@testable import Dockiva

/// CoreClient against a fake core: one unix socket that answers each request line.
final class CoreClientTests: XCTestCase {
    func testCallsErrorsAndEvents() async throws {
        let path = "/tmp/dockiva-test-\(getpid()).sock"
        unlink(path)
        let listener = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: Array(path.utf8))
            raw[path.utf8.count] = 0
        }
        _ = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        listen(listener, 1)
        defer { close(listener); unlink(path) }

        // The fake core: an event first, then a reply to each request by method.
        Thread.detachNewThread {
            let conn = accept(listener, nil, nil)
            func send(_ s: String) { _ = s.withCString { write(conn, $0, strlen($0)) } }
            send("{\"event\":\"dockiva:docker-event\",\"data\":{\"type\":\"container\"}}\n")
            var pending = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while true {
                let n = read(conn, &buffer, buffer.count)
                if n <= 0 { break }
                pending.append(contentsOf: buffer[0..<n])
                while let nl = pending.firstIndex(of: 0x0A) {
                    let line = pending[pending.startIndex..<nl]
                    pending.removeSubrange(pending.startIndex...nl)
                    let request = try! JSONSerialization.jsonObject(with: Data(line)) as! [String: Any]
                    let id = request["id"] as! Int
                    switch request["method"] as! String {
                    case "DockerService.Status":
                        send("{\"id\":\(id),\"result\":{\"connected\":true,\"apiVersion\":\"1.45\",\"osType\":\"linux\"}}\n")
                    case "Test.Echo":
                        let args = try! JSONSerialization.data(withJSONObject: request["args"]!)
                        send("{\"id\":\(id),\"result\":\(String(data: args, encoding: .utf8)!)}\n")
                    default:
                        send("{\"id\":\(id),\"error\":\"unknown method\"}\n")
                    }
                }
            }
            close(conn)
        }

        let client = try CoreClient(socket: path, timeout: 5)
        let status = try await client.call("DockerService.Status", as: DockerStatus.self)
        XCTAssertTrue(status.connected)
        XCTAssertEqual(status.apiVersion, "1.45")

        let echoed = try await client.call("Test.Echo", ["a\"b", 3], as: [AnyCodableValue].self)
        XCTAssertEqual(echoed, [.string("a\"b"), .int(3)])

        do {
            try await client.run("Nope.Missing")
            XCTFail("expected an error")
        } catch {
            XCTAssertEqual(error.localizedDescription, "unknown method")
        }

        var iterator = client.events.makeAsyncIterator()
        let event = await iterator.next()
        XCTAssertEqual(event?.name, "dockiva:docker-event")
    }

    func testStripANSI() {
        XCTAssertEqual(stripANSI("\u{1B}[1;32mok\u{1B}[0m\r\n/ # "), "ok\n/ # ")
    }
}

enum AnyCodableValue: Decodable, Equatable {
    case string(String), int(Int)
    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let i = try? c.decode(Int.self) { self = .int(i) } else { self = .string(try c.decode(String.self)) }
    }
}
