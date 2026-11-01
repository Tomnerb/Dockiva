import Foundation
import XCTest
@testable import Dockiva

/// The engine, storage and migration mirrors against JSON from the Go core
/// (observed from a running core where noted, otherwise built from the Go struct tags).
final class EngineSettingsTests: XCTestCase {
    private func decode<T: Decodable>(_ json: String, as type: T.Type = T.self) throws -> T {
        try JSONDecoder().decode(T.self, from: Data(json.utf8))
    }

    func testRuntimeOverview() throws {
        // Observed: DockerService.GetRuntimeOverview on Apple Silicon with External Docker selected.
        let overview: EngineRuntimeOverview = try decode("""
        {"active":{"provider":"docker","displayName":"Docker Engine (Moby API)","endpoint":"unix:///Users/me/.orbstack/run/docker.sock",
          "connected":true,"experimental":false,"capabilities":{"containers":true,"lifecycle":true,"stats":true,"logs":true,"terminal":true,
          "images":true,"pullImages":false,"createContainer":false,"volumes":true,"networks":true,"portPublishing":true,"dns":true,"compose":true},
          "message":"Docker Engine API 1.45 is connected."},
         "candidates":[
          {"provider":"docker","displayName":"External Docker Engine","available":true,"detected":true,"selectable":true,"experimental":false,
           "message":"Docker Desktop or another external Docker endpoint."},
          {"provider":"containerd","displayName":"containerd (native macOS guest)","available":false,"detected":false,"selectable":false,
           "experimental":true,"endpoint":"/Users/me/Library/Application Support/Dockiva/run/containerd.sock","message":"Native macOS guest is not running."},
          {"provider":"vmfree","displayName":"Dockiva Engine","available":true,"detected":false,"selectable":true,"experimental":true,
           "endpoint":"unix:///Users/me/.dockiva-engine/engine.sock","message":"Runs Linux containers natively on Apple Silicon, with no virtual machine."}]}
        """)
        XCTAssertTrue(overview.active.connected)
        XCTAssertEqual(overview.active.capabilities["compose"], true)
        XCTAssertEqual(overview.active.capabilities["pullImages"], false)
        XCTAssertEqual(overview.candidates?.map(\.backend), ["external", "vz", "vmfree"])
        XCTAssertNil(overview.candidates?[0].endpoint)
        XCTAssertFalse(overview.candidates![1].selectable)
    }

    func testPlatformAndIdentity() throws {
        // Observed: GetPlatformInfo, DockerEndpointIdentity, DiscoverDockerEndpoint.
        let platform: EnginePlatformInfo = try decode("""
        {"os":"darwin","arch":"arm64","dockerCli":"/usr/local/bin/docker","dockerHost":"unix:///Users/me/.orbstack/run/docker.sock","hostSource":"DOCKER_HOST"}
        """)
        XCTAssertEqual(platform.dockerCli, "/usr/local/bin/docker")
        XCTAssertNil(platform.dockerContext)
        let identity: [String: String] = try decode(#"{"displayName":"External Docker","endpoint":"unix:///var/run/docker.sock","kind":"external"}"#)
        XCTAssertEqual(identity["endpoint"], "unix:///var/run/docker.sock")
        XCTAssertEqual(try decode(#""unix:///var/run/docker.sock""#, as: String.self), "unix:///var/run/docker.sock")
    }

    func testEngineStatusWithoutEndpoint() throws {
        // Observed: GetEngineStatus("vz", "") with the guest not installed omits endpoint.
        let status: EngineStatus = try decode("""
        {"backend":"vz","platform":"darwin","supported":true,"helperInstalled":true,"engineInstalled":false,"running":false,
         "message":"Native helper is installed, but guest assets are missing"}
        """)
        XCTAssertNil(status.endpoint)
        XCTAssertFalse(status.running)
        let result: EngineActionResult = try decode(#"{"status":{"backend":"vz","platform":"darwin","supported":true,"helperInstalled":true,"engineInstalled":false,"running":false}}"#)
        XCTAssertNil(result.output)
    }

    func testStorage() throws {
        // GetDiskUsage: `docker system df` lines (observed from OrbStack) as DockerDiskUsageItem.
        let usage: [StorageDiskUsageItem]? = try decode("""
        [{"type":"Images","totalCount":27,"active":11,"size":"6.489GB","reclaimable":"1.196GB (18%)"},
         {"type":"Containers","totalCount":40,"active":30,"size":"325MB","reclaimable":"319.1MB (98%)"},
         {"type":"Local Volumes","totalCount":72,"active":36,"size":"89.22GB","reclaimable":"581.8MB (0%)"},
         {"type":"Build Cache","totalCount":162,"active":0,"size":"5.108GB","reclaimable":"4.527GB"}]
        """)
        XCTAssertEqual(usage?.map(\.id), ["Images", "Containers", "Local Volumes", "Build Cache"])
        XCTAssertEqual(usage?[2].reclaimable, "581.8MB (0%)")
        // A nil Go slice arrives as null.
        XCTAssertNil(try decode("null", as: [StorageDiskUsageItem]?.self))

        let folders: [StorageHostItem]? = try decode("""
        [{"id":"docker-desktop","name":"Docker container data","description":"Docker Desktop images, containers, volumes, and virtual-machine data.",
          "path":"/Users/me/Library/Containers/com.docker.docker","sizeBytes":64424509440,"size":"64.42 GB","canReveal":true,"canClean":true,
          "engineRunning":false,"cleanupLabel":"Review cleanup"}]
        """)
        XCTAssertEqual(folders?.first?.sizeBytes, 64_424_509_440)
        XCTAssertEqual(folders?.first?.engineRunning, false)
        XCTAssertEqual(try decode("[]", as: [StorageHostItem]?.self), [])  // observed with no engine folders
        XCTAssertEqual(try decode(#"{"output":"Total reclaimed space: 1.2GB"}"#, as: DockerCLIResult.self).output, "Total reclaimed space: 1.2GB")
        XCTAssertEqual(StoragePruneScope.allCases.map(\.rawValue), ["system", "containers", "images", "networks", "volumes"])  // PruneDocker's scopes
    }

    func testMigration() throws {
        // Observed: GetDockerMigrationPreview against an external engine.
        let preview: MigrationPreview = try decode("""
        {"available":true,"reachable":true,"volumeMigrationSupported":false,"endpoint":"unix:///Users/me/.orbstack/run/docker.sock",
         "containers":23,"running":0,"images":30,"volumes":44,
         "message":"Existing Docker resources were found. Migration is opt-in and leaves Docker unchanged until verification succeeds."}
        """)
        XCTAssertEqual(preview.images, 30)
        XCTAssertFalse(preview.volumeMigrationSupported)
        // With Dockiva Engine active the core returns only a message.
        let unavailable: MigrationPreview = try decode("""
        {"available":false,"reachable":false,"volumeMigrationSupported":false,"containers":0,"running":0,"images":0,"volumes":0,
         "message":"Migration into Dockiva Engine is not available yet."}
        """)
        XCTAssertNil(unavailable.endpoint)

        // Observed idle status, then a running image job and a failed container job.
        let idle: MigrationStatus = try decode(#"{"state":"","message":""}"#)
        XCTAssertFalse(idle.isRunning)
        XCTAssertNil(idle.kind)
        let progress: MigrationStatus = try decode("""
        {"state":"running","message":"Importing image 3 of 30…","importedImages":2,"totalImages":30,"currentImage":"postgres:16-alpine"}
        """)
        XCTAssertTrue(progress.isRunning)
        XCTAssertEqual(progress.importedImages, 2)
        let failed: MigrationStatus = try decode("""
        {"kind":"container","state":"failed","message":"Docker container migration failed.","error":"external Docker is offline"}
        """)
        XCTAssertEqual(failed.kind, "container")
        XCTAssertEqual(failed.error, "external Docker is offline")
    }
}
