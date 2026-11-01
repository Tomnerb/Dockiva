import AppKit
import SwiftUI

// Dockiva's look, shared with MacDoctor: a deep navy backdrop tinted by the
// section's accent, frosted surfaces with gradient strokes, rounded type.

private struct DockivaAccentKey: EnvironmentKey {
    static let defaultValue = DockivaPalette.cyan
}

extension EnvironmentValues {
    var dockivaAccent: Color {
        get { self[DockivaAccentKey.self] }
        set { self[DockivaAccentKey.self] = newValue }
    }
}

enum DockivaPalette {
    static let teal = Color(red: 0.02, green: 0.78, blue: 0.72)
    static let cyan = Color(red: 0.16, green: 0.84, blue: 0.90)
    static let blue = Color(red: 0.16, green: 0.48, blue: 0.96)
    static let navy = Color(red: 0.018, green: 0.075, blue: 0.12)
    static let deepNavy = Color(red: 0.012, green: 0.035, blue: 0.075)
    static let running = Color(red: 0.2, green: 0.86, blue: 0.5)
}

struct PrimaryButtonStyle: ButtonStyle {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.isEnabled) private var isEnabled
    var color: Color = .accentColor

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .semibold, design: .rounded))
            .foregroundStyle(.white)
            .padding(.horizontal, 16)
            .padding(.vertical, 9)
            .background(
                LinearGradient(colors: [color.opacity(configuration.isPressed ? 0.72 : 1), color.opacity(0.78)],
                               startPoint: .topLeading, endPoint: .bottomTrailing),
                in: RoundedRectangle(cornerRadius: 11, style: .continuous)
            )
            .shadow(color: color.opacity(configuration.isPressed ? 0.08 : 0.22), radius: 8, y: 4)
            .opacity(isEnabled ? 1 : 0.45)
            .scaleEffect(configuration.isPressed && !reduceMotion ? 0.98 : 1)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

struct SecondaryButtonStyle: ButtonStyle {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.isEnabled) private var isEnabled
    var destructive = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .semibold, design: .rounded))
            .foregroundStyle(destructive ? Color.red.opacity(0.9) : Color.primary)
            .padding(.horizontal, 13)
            .padding(.vertical, 8)
            .background(Color.primary.opacity(configuration.isPressed ? 0.12 : 0.065),
                        in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .opacity(isEnabled ? 1 : 0.45)
            .scaleEffect(configuration.isPressed && !reduceMotion ? 0.98 : 1)
    }
}

struct Surface: ViewModifier {
    @Environment(\.dockivaAccent) private var accent
    var cornerRadius: CGFloat = 18

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        content
            .background {
                shape.fill(.ultraThinMaterial)
                    .overlay {
                        LinearGradient(colors: [accent.opacity(0.105), .white.opacity(0.025), .clear],
                                       startPoint: .topLeading, endPoint: .bottomTrailing)
                            .clipShape(shape)
                    }
            }
            .overlay(shape.stroke(LinearGradient(colors: [accent.opacity(0.28), .white.opacity(0.07), accent.opacity(0.08)],
                                                 startPoint: .topLeading, endPoint: .bottomTrailing), lineWidth: 1))
            .shadow(color: accent.opacity(0.07), radius: 22, y: 8)
    }
}

struct InsetSurface: ViewModifier {
    @Environment(\.dockivaAccent) private var accent
    var cornerRadius: CGFloat = 14

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        content
            .background(LinearGradient(colors: [accent.opacity(0.105), .white.opacity(0.035)],
                                       startPoint: .topLeading, endPoint: .bottomTrailing), in: shape)
            .overlay(shape.stroke(accent.opacity(0.13), lineWidth: 1))
    }
}

extension View {
    func surface(cornerRadius: CGFloat = 18) -> some View { modifier(Surface(cornerRadius: cornerRadius)) }
    func insetSurface(cornerRadius: CGFloat = 14) -> some View { modifier(InsetSurface(cornerRadius: cornerRadius)) }
}

struct FeatureBackdrop: View {
    let accent: Color

    var body: some View {
        ZStack {
            DockivaPalette.deepNavy
            LinearGradient(colors: [accent.opacity(0.38), DockivaPalette.navy.opacity(0.76), accent.opacity(0.24)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
            RadialGradient(colors: [accent.opacity(0.34), accent.opacity(0.08), .clear],
                           center: .topTrailing, startRadius: 20, endRadius: 720)
            RadialGradient(colors: [accent.opacity(0.24), accent.opacity(0.04), .clear],
                           center: .bottomLeading, startRadius: 10, endRadius: 620)
            LinearGradient(colors: [.white.opacity(0.025), .clear, .black.opacity(0.16)], startPoint: .top, endPoint: .bottom)
        }
        .ignoresSafeArea()
    }
}

/// A glowing rounded tile holding an SF Symbol.
struct IconTile: View {
    let symbol: String
    let accent: Color
    var size: CGFloat = 58

    var body: some View {
        ZStack {
            Circle().fill(accent.opacity(0.16)).blur(radius: 12).frame(width: size * 1.4, height: size * 1.4)
            RoundedRectangle(cornerRadius: size * 0.31, style: .continuous)
                .fill(LinearGradient(colors: [accent.opacity(0.95), accent.opacity(0.48)], startPoint: .topLeading, endPoint: .bottomTrailing))
                .overlay(RoundedRectangle(cornerRadius: size * 0.31, style: .continuous).stroke(.white.opacity(0.25)))
            Image(systemName: symbol)
                .font(.system(size: size * 0.43, weight: .semibold))
                .foregroundStyle(.white)
        }
        .frame(width: size, height: size)
        .shadow(color: accent.opacity(0.3), radius: 13, y: 6)
    }
}

struct FeatureHeader<Actions: View>: View {
    let title: String
    let subtitle: String
    let symbol: String
    let accent: Color
    @ViewBuilder let actions: Actions

    var body: some View {
        HStack(alignment: .center, spacing: 17) {
            IconTile(symbol: symbol, accent: accent)
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.system(size: 29, weight: .bold, design: .rounded)).lineLimit(1)
                Text(subtitle).font(.subheadline).foregroundStyle(.white.opacity(0.55)).lineLimit(2)
            }
            .layoutPriority(-1)
            Spacer(minLength: 16)
            actions.fixedSize()
        }
        .padding(.horizontal, 4)
        .padding(.vertical, 8)
    }
}

struct SectionTitle: View {
    let title: String
    let detail: String
    let symbol: String
    @Environment(\.dockivaAccent) private var accent

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 14, weight: .bold))
                .foregroundStyle(accent)
                .frame(width: 34, height: 34)
                .background(accent.opacity(0.13), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.headline)
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

struct MetricCard: View {
    @Environment(\.dockivaAccent) private var accent
    let title: String
    let value: String
    let symbol: String
    let subtitle: String
    var progress: Double?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Image(systemName: symbol)
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(accent)
                    .frame(width: 30, height: 30)
                    .background(accent.opacity(0.13), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                Text(title).font(.subheadline.weight(.semibold))
                Spacer()
            }
            Text(value).font(.system(size: 26, weight: .bold, design: .rounded)).monospacedDigit().lineLimit(1).minimumScaleFactor(0.6)
            if let progress {
                ProgressView(value: min(max(progress, 0), 1)).tint(accent)
            }
            Text(subtitle).font(.caption).foregroundStyle(.secondary).lineLimit(2)
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(cornerRadius: 18)
    }
}

struct FilterPills<Selection: Hashable>: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    let choices: [(label: String, value: Selection)]
    @Binding var selection: Selection

    var body: some View {
        HStack(spacing: 6) {
            ForEach(Array(choices.enumerated()), id: \.offset) { _, choice in
                let selected = selection == choice.value
                Button {
                    withAnimation(reduceMotion ? nil : .easeOut(duration: 0.14)) { selection = choice.value }
                } label: {
                    Text(choice.label)
                        .font(.system(size: 12, weight: .semibold, design: .rounded))
                        .lineLimit(1)
                        .fixedSize()
                        .foregroundStyle(selected ? Color.white : Color.secondary)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(selected ? Color.accentColor : Color.clear, in: Capsule())
                }
                .buttonStyle(.plain)
            }
        }
        .padding(4)
        .background(Color.primary.opacity(0.055), in: Capsule())
        .fixedSize()
    }
}

struct EmptyStateCard: View {
    let eyebrow: String
    let title: String
    let detail: String
    let symbol: String
    let accent: Color

    var body: some View {
        HStack(spacing: 28) {
            ZStack {
                Circle().fill(accent.opacity(0.1)).frame(width: 112, height: 112)
                Circle().stroke(accent.opacity(0.2), lineWidth: 1).frame(width: 92, height: 92)
                IconTile(symbol: symbol, accent: accent, size: 64)
            }
            VStack(alignment: .leading, spacing: 7) {
                Text(eyebrow).font(.caption2.weight(.bold)).tracking(1.5).foregroundStyle(accent)
                Text(title).font(.title2.bold())
                Text(detail).font(.subheadline).foregroundStyle(.secondary).lineSpacing(3).frame(maxWidth: 520, alignment: .leading)
            }
            Spacer()
        }
        .frame(maxWidth: .infinity, minHeight: 170)
        .padding(.horizontal, 30)
        .padding(.vertical, 22)
        .surface(cornerRadius: 24)
    }
}

/// A search field in MacDoctor's style.
struct SearchField: View {
    let prompt: String
    @Binding var text: String

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField(prompt, text: $text).textFieldStyle(.plain)
            if !text.isEmpty {
                Button { text = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                    .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 11)
        .padding(.vertical, 8)
        .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

struct StatusDot: View {
    let running: Bool

    var body: some View {
        Circle()
            .fill(running ? DockivaPalette.running : Color.secondary.opacity(0.5))
            .frame(width: 8, height: 8)
            .shadow(color: running ? DockivaPalette.running : .clear, radius: 4)
    }
}

/// The app icon (icons.icns in the bundle; the generic app icon in a bare build).
struct AppIconImage: View {
    var body: some View {
        Image(nsImage: NSApplication.shared.applicationIconImage).resizable().interpolation(.high)
    }
}
