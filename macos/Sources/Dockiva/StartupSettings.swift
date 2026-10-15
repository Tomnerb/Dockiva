import AppKit
import ServiceManagement
import SwiftUI

/// Whether the main window opens at launch: not with --hidden (the login item the
/// Wails app registered passes it) or when the user chose to start in the menu bar.
func shouldOpenWindowAtLaunch(settings: AppSettings?, arguments: [String] = CommandLine.arguments) -> Bool {
    !arguments.contains("--hidden") && settings?.startHidden != true
}

/// Settings › Startup: open at login, start hidden, and the app version.
struct StartupSettingsSection: View {
    @Environment(AppModel.self) private var model
    @State private var loginStatus = SMAppService.mainApp.status
    @State private var loginError: String?
    @State private var version: VersionInfo?

    var body: some View {
        Section("Startup") {
            Toggle("Open at login", isOn: Binding(get: { loginStatus == .enabled || loginStatus == .requiresApproval }, set: setOpenAtLogin))
                // Approval happens in System Settings; reread the status on return.
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                    loginStatus = SMAppService.mainApp.status
                }
            if loginStatus == .requiresApproval {
                HStack {
                    Text("Allow Dockiva in System Settings › General › Login Items.").font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button("Open Login Items") { SMAppService.openSystemSettingsLoginItems() }.controlSize(.small)
                }
            }
            if let loginError {
                Text(loginError).font(.caption).foregroundStyle(.red)
            }
            Toggle("Start hidden in the menu bar", isOn: Binding(get: { model.settings?.startHidden ?? false }, set: { value in
                guard var next = model.settings else { return }
                next.startHidden = value
                Task { await model.updateSettings(next) }
            }))
            .disabled(model.settings == nil)
            LabeledContent("Version", value: version?.version ?? "—")
                .task(id: model.phase) {
                    version = try? await model.core?.call("AppService.GetVersionInfo", as: VersionInfo.self)
                }
        }
        .toggleStyle(.switch)
    }

    private func setOpenAtLogin(_ on: Bool) {
        loginError = nil
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {
            loginError = error.localizedDescription
        }
        loginStatus = SMAppService.mainApp.status
    }
}
