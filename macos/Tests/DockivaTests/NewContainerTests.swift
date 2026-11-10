import Foundation
import XCTest
@testable import Dockiva

final class NewContainerTests: XCTestCase {
    func testImageShortNames() {
        XCTAssertEqual(ImageStyle.name("postgres:16-alpine"), "postgres")
        XCTAssertEqual(ImageStyle.name("docker.io/library/redis:7"), "redis")
        XCTAssertEqual(ImageStyle.name("ghcr.io/org/node@sha256:abc"), "node")
        XCTAssertEqual(ImageStyle.name("localhost:5000/app"), "app")
    }

    func testKnownImagesGetTheirIcon() {
        XCTAssertEqual(ImageStyle.of("postgres:16-alpine").symbol, "cylinder.split.1x2.fill")
        XCTAssertEqual(ImageStyle.of("bitnami/redis").symbol, "square.stack.3d.up.fill")
        XCTAssertEqual(ImageStyle.of("node:22-alpine").symbol, "hexagon.fill")
        XCTAssertEqual(ImageStyle.of("sha256:7e92bb0dd372").symbol, "shippingbox.fill")
    }

    /// The JSON must match the Go core's RuntimeCreateContainerRequest.
    func testCreateRequestJSON() throws {
        let request = CreateContainerRequest(name: "pg", image: "postgres:16-alpine", command: [], autoStart: true,
                                             portMappings: [.init(hostPort: 5432, containerPort: 5432)], env: ["POSTGRES_PASSWORD=x"])
        let object = try JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as! [String: Any]
        XCTAssertEqual(Set(object.keys), ["name", "image", "command", "autoStart", "portMappings", "env"])
        let port = (object["portMappings"] as! [[String: Any]])[0]
        XCTAssertEqual(port["hostPort"] as? Int, 5432)
        XCTAssertEqual(port["protocol"] as? String, "tcp")
        XCTAssertEqual(port["hostIp"] as? String, "127.0.0.1")
    }
}
