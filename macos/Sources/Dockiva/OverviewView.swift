import AppKit
import SwiftUI

/// The landing screen: engine state as a hero with one big action, live totals,
/// and the containers that are running now.
struct OverviewView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var orbit = 0.0
    @State private var floating = false
    @State private var glow = false

    private var connected: Bool { model.status?.connected == true }
    private let accent = SidebarItem.overview.accent

    var body: some View {
        ScrollView(showsIndicators: false) {
            VStack(spacing: 20) {
                topBar
                hero
                message
                primaryAction
                metrics
                runningNow
                quickActions
            }
            .frame(maxWidth: 980)
            .padding(.horizontal, 42)
            .padding(.vertical, 24)
            .frame(maxWidth: .infinity)
        }
        .task(id: reduceMotion) {
            guard !reduceMotion else { return }
            withAnimation(.linear(duration: 14).repeatForever(autoreverses: false)) { orbit = 360 }
            withAnimation(.easeInOut(duration: 3).repeatForever(autoreverses: true)) { floating = true }
            withAnimation(.easeInOut(duration: 2.2).repeatForever(autoreverses: true)) { glow = true }
        }
    }

    private var topBar: some View {
        HStack {
            Label("OVERVIEW", systemImage: "sparkles")
                .font(.system(size: 12, weight: .bold, design: .rounded))
                .tracking(1.2)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
                .background(.white.opacity(0.08), in: Capsule())
                .overlay(Capsule().stroke(.white.opacity(0.1)))
            Spacer()
            Label("No virtual machine", systemImage: "cpu")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.white.opacity(0.7))
                .padding(.horizontal, 12)
                .padding(.vertical, 7)
                .background(.white.opacity(0.06), in: Capsule())
        }
    }

    private var hero: some View {
        ZStack {
            Circle().fill(accent.opacity(glow ? 0.22 : 0.12)).blur(radius: 40).frame(width: 260, height: 260)
            Ellipse()
                .stroke(LinearGradient(colors: [accent.opacity(0.7), .clear, DockivaPalette.blue.opacity(0.6)], startPoint: .leading, endPoint: .trailing), lineWidth: 2)
                .frame(width: 300, height: 92)
                .rotationEffect(.degrees(-12))
            ForEach(0..<3) { i in
                Circle()
                    .fill(i == 1 ? DockivaPalette.blue : accent)
                    .frame(width: CGFloat(10 - i * 2), height: CGFloat(10 - i * 2))
                    .shadow(color: accent, radius: 6)
                    .offset(x: 150)
                    .rotationEffect(.degrees(orbit + Double(i) * 120))
                    .scaleEffect(x: 1, y: 0.31)
                    .rotationEffect(.degrees(-12))
            }
            ZStack {
                RoundedRectangle(cornerRadius: 40, style: .continuous)
                    .fill(LinearGradient(colors: [accent, DockivaPalette.blue.opacity(0.85), DockivaPalette.navy], startPoint: .topLeading, endPoint: .bottomTrailing))
                    .overlay(RoundedRectangle(cornerRadius: 40, style: .continuous).stroke(.white.opacity(0.35), lineWidth: 1.5))
                    .shadow(color: accent.opacity(0.45), radius: 30, y: 12)
                Image(systemName: connected ? "shippingbox.fill" : "shippingbox")
                    .font(.system(size: 64, weight: .semibold))
                    .foregroundStyle(.white)
                    .shadow(color: .black.opacity(0.25), radius: 6, y: 3)
            }
            .frame(width: 150, height: 150)
            .offset(y: floating ? -6 : 6)
        }
        .frame(height: 250)
    }

    private var message: some View {
        VStack(spacing: 8) {
            Text(connected ? "Your containers are running" : "Containers, without the VM")
                .font(.system(size: 32, weight: .semibold, design: .rounded))
            Text(connected
                 ? "\(model.runningCount) of \(model.containers.count) containers up, \(model.images.count) images, \(model.volumes.count) volumes."
                 : "Start the engine to run Linux containers natively on this Mac, with the Docker CLI and Compose you already use.")
                .font(.body)
                .foregroundStyle(.white.opacity(0.62))
                .multilineTextAlignment(.center)
                .frame(maxWidth: 560)
        }
    }

    private var primaryAction: some View {
        Button {
            if connected { model.section = .containers } else { Task { await model.engineAction(start: true) } }
        } label: {
            ZStack {
                Circle()
                    .fill(LinearGradient(colors: [DockivaPalette.cyan, DockivaPalette.blue], startPoint: .topLeading, endPoint: .bottomTrailing))
                    .overlay(Circle().stroke(.white.opacity(0.35), lineWidth: 1.5))
                    .shadow(color: DockivaPalette.cyan.opacity(glow ? 0.55 : 0.3), radius: glow ? 26 : 16)
                if model.engineBusy {
                    ProgressView().controlSize(.large).tint(.white)
                } else {
                    VStack(spacing: 6) {
                        Image(systemName: connected ? "square.grid.2x2.fill" : "power").font(.system(size: 22, weight: .semibold))
                        Text(connected ? "Containers" : "Start").font(.system(size: 15, weight: .semibold, design: .rounded))
                    }
                    .foregroundStyle(.white)
                }
            }
            .frame(width: 112, height: 112)
        }
        .buttonStyle(.plain)
        .disabled(model.engineBusy)
        .accessibilityLabel(connected ? "Show containers" : "Start engine")
    }

    private var metrics: some View {
        HStack(spacing: 14) {
            MetricCard(title: "Running", value: "\(model.runningCount)", symbol: "play.circle.fill",
                       subtitle: "\(model.containers.count - model.runningCount) stopped")
            MetricCard(title: "CPU", value: String(format: "%.1f%%", model.totalCPU), symbol: "cpu",
                       subtitle: "Across running containers",
                       progress: model.totalCPU / 100 / Double(ProcessInfo.processInfo.activeProcessorCount))
            MetricCard(title: "Memory", value: model.totalMemory.byteString, symbol: "memorychip",
                       subtitle: "Of \(ProcessInfo.processInfo.physicalMemory.byteString) on this Mac",
                       progress: Double(model.totalMemory) / Double(ProcessInfo.processInfo.physicalMemory))
            MetricCard(title: "Images", value: model.imageBytes.byteString, symbol: "square.stack.3d.up.fill",
                       subtitle: "\(model.images.count) images stored")
        }
    }

    @ViewBuilder private var runningNow: some View {
        let running = model.containers.filter(\.isRunning)
            .sorted { (model.stats[$0.id]?.cpuPercent ?? 0) > (model.stats[$1.id]?.cpuPercent ?? 0) }
        if connected && !running.isEmpty {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    SectionTitle(title: "Running now", detail: "Busiest first", symbol: "waveform.path.ecg")
                    Spacer()
                    Button("All containers") { model.section = .containers }.buttonStyle(SecondaryButtonStyle())
                }
                VStack(spacing: 4) {
                    ForEach(running.prefix(6)) { container in
                        ContainerSummaryRow(container: container, stats: model.stats[container.id])
                    }
                }
            }
            .padding(20)
            .surface(cornerRadius: 22)
        }
    }

    private var quickActions: some View {
        HStack(spacing: 10) {
            chip("Pull an image", "arrow.down.circle") { model.section = .images }
            chip("Volumes", "externaldrive") { model.section = .volumes }
            chip("Networks", "network") { model.section = .networks }
            if let endpoint = model.engine?.endpoint {
                chip("Copy DOCKER_HOST", "doc.on.doc") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString("export DOCKER_HOST=\(endpoint)", forType: .string)
                }
            }
        }
    }

    private func chip(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label(title, systemImage: symbol)
                .font(.caption.weight(.semibold))
                .padding(.horizontal, 12)
                .padding(.vertical, 7)
                .background(.white.opacity(0.07), in: Capsule())
                .overlay(Capsule().stroke(.white.opacity(0.08)))
        }
        .buttonStyle(.plain)
        .foregroundStyle(.white.opacity(0.8))
    }
}

/// One container with its live usage, used on the overview.
struct ContainerSummaryRow: View {
    let container: ContainerInfo
    let stats: ContainerResourceStats?

    var body: some View {
        HStack(spacing: 12) {
            StatusDot(running: container.isRunning)
            VStack(alignment: .leading, spacing: 1) {
                Text(container.name).font(.system(size: 14, weight: .semibold, design: .rounded)).lineLimit(1)
                Text(container.image).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            Text(stats.map { String(format: "%.1f%%", $0.cpuPercent) } ?? "—")
                .font(.callout.monospacedDigit()).frame(width: 64, alignment: .trailing)
            Text(stats?.memoryUsage.byteString ?? "—")
                .font(.callout.monospacedDigit()).foregroundStyle(.secondary).frame(width: 84, alignment: .trailing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .insetSurface(cornerRadius: 12)
    }
}
