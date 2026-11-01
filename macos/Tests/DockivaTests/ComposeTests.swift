import Foundation
import XCTest
@testable import Dockiva

/// The Compose and inspect mirrors against JSON shaped like the Go core's
/// (the ContainerDetails samples are what a live core returned).
final class ComposeTests: XCTestCase {
    func testDecodesContainerDetails() throws {
        // Go nil slices arrive as null (mounts here).
        let nginx = """
        {"id":"8635c46c81ed9574be23513a4e4bd371b6f2791c94b61418cd9d44440d1c10ce","name":"sw-nginx",
         "image":"sha256:84dd96a0337ae0f15be81afa8c6bd99d06a514a2ab71a1503aba91ce4392ecc2",
         "created":"2026-10-14T16:16:07.243087872Z","path":"/bin/sh",
         "args":["/docker-entrypoint.sh","nginx","-g","daemon off;"],"entrypoint":["/bin/sh"],
         "command":["/docker-entrypoint.sh","nginx","-g","daemon off;"],"workingDir":"/","user":"",
         "environment":["PATH=/usr/local/sbin:/usr/local/bin","NGINX_VERSION=1.31.6"],"mounts":null,
         "networks":[{"name":"bridge","ipAddress":"10.89.0.2","gateway":"10.89.0.1","mac":""}],
         "labels":{},"restartPolicy":"no","state":"exited","exitCode":131,
         "startedAt":"2026-10-14T16:16:08.182582016Z","finishedAt":"2026-10-14T17:00:29.778469888Z"}
        """
        let d = try JSONDecoder().decode(ContainerDetails.self, from: Data(nginx.utf8))
        XCTAssertEqual(d.name, "sw-nginx")
        XCTAssertNil(d.mounts)
        XCTAssertEqual(d.networks?.first?.ipAddress, "10.89.0.2")
        XCTAssertEqual(d.exitCode, 131)
        XCTAssertEqual(ContainerDetails.commandLine(d.command), "/docker-entrypoint.sh nginx -g \"daemon off;\"")

        let mongo = """
        {"id":"178462b5821512e290c795a03fa3f1ce88093596cbcff3838059dc06b7543718","name":"sw-mongo","image":"sha256:1",
         "created":"2026-10-14T16:16:07Z","path":"docker-entrypoint.sh","args":null,"entrypoint":null,"command":null,
         "workingDir":"","user":"999","environment":null,
         "mounts":[{"type":"volume","name":"83917620b67f","source":"/Users/me/.vmfree/volumes/83917620b67f/_data","destination":"/data/configdb","rw":true},
                   {"type":"bind","source":"/Users/me/app","destination":"/app","rw":false}],
         "networks":null,"labels":{"com.docker.compose.project":"shop","com.docker.compose.service":"db"},
         "restartPolicy":"unless-stopped","state":"running","exitCode":0,
         "startedAt":"2026-10-14T16:16:08.182582016Z","finishedAt":"0001-01-01T00:00:00Z"}
        """
        let m = try JSONDecoder().decode(ContainerDetails.self, from: Data(mongo.utf8))
        XCTAssertEqual(m.mounts?.count, 2)
        XCTAssertEqual(m.mounts?[0].name, "83917620b67f")
        XCTAssertNil(m.mounts?[1].name)
        XCTAssertEqual(m.mounts?[1].rw, false)
        XCTAssertEqual(m.labels?["com.docker.compose.service"], "db")
        XCTAssertNil(m.environment)
    }

    func testDecodesComposeTypes() throws {
        let details = try JSONDecoder().decode(ComposeProjectDetails.self, from: Data("""
        {"project":"shop","workingDir":"/Users/me/shop","configFiles":["/Users/me/shop/compose.yaml","/Users/me/shop/compose.override.yaml"]}
        """.utf8))
        XCTAssertEqual(details.configFiles?.count, 2)
        XCTAssertNil(details.envFile)  // omitempty

        let bare = try JSONDecoder().decode(ComposeProjectDetails.self, from: Data("""
        {"project":"legacy","workingDir":"","configFiles":null,"envFile":"/Users/me/legacy/.env"}
        """.utf8))
        XCTAssertNil(bare.configFiles)
        XCTAssertEqual(bare.envFile, "/Users/me/legacy/.env")

        let imported = try JSONDecoder().decode(ComposeImportResult.self, from: Data("""
        {"project":"shop","workingDir":"/Users/me/shop","configFiles":["/Users/me/shop/compose.yaml"],"output":" Container shop-web-1  Started"}
        """.utf8))
        XCTAssertEqual(imported.project, "shop")
        XCTAssertTrue(imported.output.contains("Started"))
    }

    func testEnvironmentPairsAndTimes() {
        var d = try! JSONDecoder().decode(ContainerDetails.self, from: Data("""
        {"id":"x","name":"x","image":"x","created":"","path":"","workingDir":"","user":"","restartPolicy":"","state":"created",
         "exitCode":0,"startedAt":"","finishedAt":""}
        """.utf8))
        d.environment = ["URL=postgres://u:p@db/x?a=b", "EMPTY=", "BARE"]
        let pairs = d.environmentPairs
        XCTAssertEqual(pairs.map(\.key), ["URL", "EMPTY", "BARE"])
        XCTAssertEqual(pairs.map(\.value), ["postgres://u:p@db/x?a=b", "", ""])

        XCTAssertEqual(ContainerDetails.date("2026-10-14T16:16:07.243087872Z"), ISO8601DateFormatter().date(from: "2026-10-14T16:16:07Z"))
        XCTAssertNotNil(ContainerDetails.date("2026-10-14T23:16:07+07:00"))
        XCTAssertNil(ContainerDetails.date("0001-01-01T00:00:00Z"))
        XCTAssertNil(ContainerDetails.date(""))
    }
}
