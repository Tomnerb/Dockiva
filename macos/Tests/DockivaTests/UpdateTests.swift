import Foundation
import XCTest
@testable import Dockiva

final class UpdateTests: XCTestCase {
    func testDecodesUpdateInfo() throws {
        let json = """
        {"currentVersion":"0.4.1","latestVersion":"0.5.0","available":true,"releaseName":"Dockiva 0.5.0",
         "releaseUrl":"https://github.com/Tomnerb/Dockiva/releases/tag/v0.5.0","publishedAt":"2026-10-01T12:30:00Z",
         "notes":"## What's Changed\\n* Native app","message":"Dockiva 0.5.0 is available.",
         "artifactName":"Dockiva-macos-arm64.zip","artifactSize":48211234}
        """
        let info = try JSONDecoder().decode(UpdateInfo.self, from: Data(json.utf8))
        XCTAssertTrue(info.available)
        XCTAssertEqual(info.latestVersion, "0.5.0")
        XCTAssertEqual(info.releaseUrl, "https://github.com/Tomnerb/Dockiva/releases/tag/v0.5.0")
        XCTAssertEqual(info.artifactSize, 48_211_234)
        XCTAssertEqual(info.publishedDate, Date(timeIntervalSince1970: 1_790_857_800))
        XCTAssertEqual(String(releaseNotes(info.notes).characters), "What's Changed\n• Native app")
    }

    func testDecodesUpToDateInfo() throws {
        // CheckForUpdates when there is no newer release: Go's zero values.
        let json = """
        {"currentVersion":"0.4.1","latestVersion":"0.4.1","available":false,"releaseName":"","releaseUrl":"",
         "publishedAt":"","notes":"","message":"Dockiva is up to date.","artifactName":"","artifactSize":0}
        """
        let info = try JSONDecoder().decode(UpdateInfo.self, from: Data(json.utf8))
        XCTAssertFalse(info.available)
        XCTAssertNil(info.publishedDate)
    }

    func testDecodesVersionInfo() throws {
        let json = #"{"version":"0.4.1","commit":"8df5118","buildDate":"2026-10-14","channel":"stable"}"#
        let info = try JSONDecoder().decode(VersionInfo.self, from: Data(json.utf8))
        XCTAssertEqual(info, VersionInfo(version: "0.4.1", commit: "8df5118", buildDate: "2026-10-14", channel: "stable"))
    }

    func testShouldOpenWindowAtLaunch() {
        var settings = AppSettings(settingsVersion: 2, engineBackend: "vmfree", runtimeProvider: "vmfree", dockerEndpoint: "",
                                   wslDistro: "", closeToTray: true, startAtLogin: false, startHidden: false,
                                   autoReconnectEngine: true, startEngineOnLaunch: true)
        XCTAssertTrue(shouldOpenWindowAtLaunch(settings: nil, arguments: ["Dockiva"]))
        XCTAssertTrue(shouldOpenWindowAtLaunch(settings: settings, arguments: ["Dockiva"]))
        XCTAssertFalse(shouldOpenWindowAtLaunch(settings: nil, arguments: ["Dockiva", "--hidden"]))
        settings.startHidden = true
        XCTAssertFalse(shouldOpenWindowAtLaunch(settings: settings, arguments: ["Dockiva"]))
    }
}
