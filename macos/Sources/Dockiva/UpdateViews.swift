import AppKit
import SwiftUI

/// The update sheet: the result of a check, the release and its notes, and install progress.
struct UpdateSheet: View {
    @Environment(UpdateCenter.self) private var updates
    private let accent = DockivaPalette.cyan

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            if updates.phase == .checking {
                checking
            } else if let info = updates.available {
                release(info)
            } else if let info = updates.latest {
                upToDate(info)
            } else {
                checking
            }
        }
        .padding(26)
        .frame(width: 580)
        .background(FeatureBackdrop(accent: accent))
        .environment(\.dockivaAccent, accent)
    }

    private var checking: some View {
        HStack(spacing: 12) {
            ProgressView().controlSize(.small)
            Text("Checking for updates…").font(.headline)
            Spacer()
            Button("Later") { updates.sheetPresented = false }
                .buttonStyle(SecondaryButtonStyle())
                .keyboardShortcut(.cancelAction)
        }
    }

    private func upToDate(_ info: UpdateInfo) -> some View {
        VStack(alignment: .leading, spacing: 20) {
            header("Dockiva is up to date", "Version \(info.currentVersion) is the newest version.", symbol: "checkmark.seal.fill", tint: DockivaPalette.running)
            HStack {
                Spacer()
                Button("OK") { updates.sheetPresented = false }
                    .buttonStyle(PrimaryButtonStyle(color: accent))
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    private func release(_ info: UpdateInfo) -> some View {
        let installing = updates.phase == .downloading || updates.phase == .installing
        let date = info.publishedDate.map { "Released \($0.formatted(date: .long, time: .omitted))" } ?? ""
        return VStack(alignment: .leading, spacing: 18) {
            header("Dockiva \(info.latestVersion) is available", "You have \(info.currentVersion).", symbol: "arrow.down.circle.fill", tint: accent)
            SectionTitle(title: info.releaseName.isEmpty ? "Dockiva \(info.latestVersion)" : info.releaseName, detail: date, symbol: "doc.text")
            ScrollView {
                Text(info.notes.isEmpty ? AttributedString("No release notes.") : releaseNotes(info.notes))
                    .lineSpacing(3)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(16)
            }
            .frame(height: 260)
            .insetSurface()
            if updates.phase == .downloading, let progress = updates.progress {
                ProgressView(value: progress).tint(accent)
            }
            HStack(spacing: 10) {
                if let url = URL(string: info.releaseUrl) {
                    Button { NSWorkspace.shared.open(url) } label: { Label("View on GitHub", systemImage: "arrow.up.right.square") }
                        .buttonStyle(SecondaryButtonStyle())
                }
                Spacer()
                Button("Later") { updates.sheetPresented = false }
                    .buttonStyle(SecondaryButtonStyle())
                    .keyboardShortcut(.cancelAction)
                Button { Task { await updates.install() } } label: {
                    HStack(spacing: 8) {
                        if installing { ProgressView().controlSize(.small) }
                        Text(updates.phase == .downloading ? "Downloading…" : updates.phase == .installing ? "Installing…" : "Install and Relaunch")
                    }
                }
                .buttonStyle(PrimaryButtonStyle(color: accent))
                .keyboardShortcut(.defaultAction)
                .disabled(installing)
            }
        }
    }

    private func header(_ title: String, _ subtitle: String, symbol: String, tint: Color) -> some View {
        HStack(spacing: 16) {
            IconTile(symbol: symbol, accent: tint, size: 52)
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.system(size: 22, weight: .bold, design: .rounded))
                Text(subtitle).font(.subheadline).foregroundStyle(.white.opacity(0.55))
            }
        }
    }
}

/// A compact notice for the bottom of the sidebar; hidden when there is no update.
struct UpdateBanner: View {
    @Environment(UpdateCenter.self) private var updates
    @Environment(\.dockivaAccent) private var accent

    var body: some View {
        if let info = updates.available {
            Button { updates.sheetPresented = true } label: {
                HStack(spacing: 9) {
                    Image(systemName: "arrow.down.circle.fill").font(.system(size: 15, weight: .semibold)).foregroundStyle(accent)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Update available — \(info.latestVersion)").font(.caption.weight(.semibold))
                        Text(status).font(.caption2).foregroundStyle(.white.opacity(0.45))
                    }
                    Spacer(minLength: 0)
                }
                .foregroundStyle(.white.opacity(0.82))
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(accent.opacity(0.14), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 13, style: .continuous).stroke(accent.opacity(0.35), lineWidth: 1))
                .contentShape(RoundedRectangle(cornerRadius: 13, style: .continuous))
            }
            .buttonStyle(.plain)
            .help("See what's new in Dockiva \(info.latestVersion)")
        }
    }

    private var status: String {
        switch updates.phase {
        case .downloading: "Downloading…"
        case .installing: "Installing…"
        default: "See what's new"
        }
    }
}
