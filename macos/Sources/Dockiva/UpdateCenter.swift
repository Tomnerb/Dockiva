import AppKit
import Foundation
import Observation
import SwiftUI

// Mirrors of app_version.go.

struct VersionInfo: Codable, Hashable {
    var version: String
    var commit: String
    var buildDate: String
    var channel: String
}

struct UpdateInfo: Codable, Hashable {
    var currentVersion: String
    var latestVersion: String
    var available: Bool
    var releaseName: String
    var releaseUrl: String
    var publishedAt: String  // "2006-01-02T15:04:05Z", or "" when up to date
    var notes: String        // Markdown
    var message: String
    var artifactName: String
    var artifactSize: Int64

    var publishedDate: Date? { ISO8601DateFormatter().date(from: publishedAt) }
}

/// The payload of the Wails updater's download-progress event (pkg/updater/types.go).
struct UpdateProgress: Codable, Hashable {
    var written: Int64
    var total: Int64
}

enum UpdateEvents {
    static let quitForUpdate = "dockiva:quit-for-update"
    static let downloadProgress = "wails:updater:download-progress"
    static let verifying = "wails:updater:verifying"
    static let installing = "wails:updater:installing"
}

/// Checks GitHub for a newer Dockiva through the core, 15 s after launch and then
/// daily, and installs it: the core downloads, verifies and stages the update,
/// starts a helper and sends dockiva:quit-for-update; the app then quits so the
/// helper can replace Dockiva.app and reopen it.
@MainActor @Observable
final class UpdateCenter {
    enum Phase: Equatable { case idle, checking, downloading, installing }

    var latest: UpdateInfo?
    var phase: Phase = .idle
    var progress: Double?  // download fraction, when the core reports it
    var sheetPresented = false

    private var model: AppModel?
    private var quitting = false

    /// The latest release when it is newer than this app.
    var available: UpdateInfo? { latest?.available == true ? latest : nil }

    /// Subscribes to the core's update events and starts the schedule. Call once the model has started.
    func start(_ model: AppModel) {
        guard self.model == nil else { return }
        self.model = model
        model.onEvent(name: UpdateEvents.quitForUpdate) { [weak self] _ in self?.quit() }
        model.onEvent(name: UpdateEvents.downloadProgress) { [weak self] data in
            guard let self, phase == .downloading,
                  let p = try? JSONDecoder().decode(UpdateProgress.self, from: data), p.total > 0 else { return }
            progress = Double(p.written) / Double(p.total)
        }
        for name in [UpdateEvents.verifying, UpdateEvents.installing] {
            model.onEvent(name: name) { [weak self] _ in
                if self?.phase == .downloading { self?.phase = .installing }
            }
        }
        Task {
            try? await Task.sleep(for: .seconds(15))
            while !Task.isCancelled {
                await checkNow(userInitiated: false)
                try? await Task.sleep(for: .seconds(24 * 60 * 60))
            }
        }
    }

    /// A user-initiated check opens the sheet, which shows the result ("Dockiva is
    /// up to date" or the release) and reports errors; a background check is silent.
    func checkNow(userInitiated: Bool) async {
        if userInitiated { sheetPresented = true }
        guard phase == .idle else { return }  // already checking or installing: the sheet shows it
        guard let core = model?.core else {
            if userInitiated { fail("The Dockiva core is not running") }
            return
        }
        phase = .checking
        defer { phase = .idle }
        do {
            latest = try await core.call("AppService.CheckForUpdates", as: UpdateInfo.self)
        } catch {
            if userInitiated { fail(error.localizedDescription) }
        }
    }

    func install() async {
        guard let core = model?.core, phase == .idle else { return }
        phase = .downloading
        progress = nil
        do {
            try await core.run("AppService.ApplyUpdate")
            quit()  // normally dockiva:quit-for-update arrived first
        } catch {
            guard !quitting else { return }  // the core exiting for the update closed the connection
            phase = .idle
            fail(error.localizedDescription)
        }
    }

    private func quit() {
        quitting = true
        NSApp.terminate(nil)
    }

    /// Closes the sheet first: macOS queues the error alert behind an open sheet.
    private func fail(_ message: String) {
        sheetPresented = false
        model?.lastError = message
    }
}

/// "Check for Updates…" after "About Dockiva" in the app menu.
struct UpdateCommands: Commands {
    let updates: UpdateCenter
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(after: .appInfo) {
            Button("Check for Updates…") {
                openWindow(id: "main")  // the sheet needs the window, which may be closed
                Task { await updates.checkNow(userInitiated: true) }
            }
        }
    }
}

/// Release notes for display: SwiftUI's Text renders only inline Markdown, so
/// headings become bold lines and list items bullets before parsing.
func releaseNotes(_ markdown: String) -> AttributedString {
    let lines = markdown.components(separatedBy: .newlines).map { line -> String in
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.hasPrefix("#") {
            let title = trimmed.drop { $0 == "#" }.trimmingCharacters(in: .whitespaces)
            return title.isEmpty ? "" : "**\(title)**"
        }
        if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") { return "• " + trimmed.dropFirst(2) }
        return line
    }
    let text = lines.joined(separator: "\n")
    return (try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
        ?? AttributedString(text)
}
