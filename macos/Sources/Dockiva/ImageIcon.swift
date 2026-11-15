import SwiftUI

/// A recognisable glyph and colour for well-known images (postgres, redis, node, …),
/// so a list of containers reads at a glance. Unknown images get a plain box.
struct ImageIcon: View {
    let image: String
    var running = false
    var size: CGFloat = 30

    var body: some View {
        let style = ImageStyle.of(image)
        ZStack(alignment: .bottomTrailing) {
            RoundedRectangle(cornerRadius: size * 0.3, style: .continuous)
                .fill(LinearGradient(colors: [style.color.opacity(0.95), style.color.opacity(0.6)], startPoint: .topLeading, endPoint: .bottomTrailing))
                .overlay(RoundedRectangle(cornerRadius: size * 0.3, style: .continuous).stroke(.white.opacity(0.18)))
                .overlay(Image(systemName: style.symbol).font(.system(size: size * 0.46, weight: .semibold)).foregroundStyle(.white))
                .frame(width: size, height: size)
                .opacity(running ? 1 : 0.55)
            StatusDot(running: running)
                .overlay(Circle().stroke(DockivaPalette.deepNavy, lineWidth: 2))
                .offset(x: 3, y: 3)
        }
        .accessibilityHidden(true)
    }
}

struct ImageStyle {
    let symbol: String
    let color: Color

    /// The image's short name: "docker.io/library/postgres:16-alpine" → "postgres".
    static func name(_ image: String) -> String {
        var last = image.split(separator: "/").last.map(String.init) ?? image
        if let at = last.firstIndex(of: "@") { last = String(last[..<at]) }
        if let colon = last.firstIndex(of: ":") { last = String(last[..<colon]) }
        return last.lowercased()
    }

    static func of(_ image: String) -> ImageStyle {
        let name = name(image)
        for (keys, style) in known where keys.contains(where: { name.hasPrefix($0) }) {
            return style
        }
        return ImageStyle(symbol: "shippingbox.fill", color: .gray)
    }

    private static func rgb(_ hex: UInt32) -> Color {
        Color(red: Double(hex >> 16 & 0xFF) / 255, green: Double(hex >> 8 & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }

    private static let known: [([String], ImageStyle)] = [
        (["postgres", "postgis", "timescale"], ImageStyle(symbol: "cylinder.split.1x2.fill", color: rgb(0x336791))),
        (["mysql", "mariadb", "percona"], ImageStyle(symbol: "cylinder.fill", color: rgb(0x00758F))),
        (["mongo"], ImageStyle(symbol: "leaf.fill", color: rgb(0x47A248))),
        (["redis", "valkey", "keydb", "dragonfly"], ImageStyle(symbol: "square.stack.3d.up.fill", color: rgb(0xDC382D))),
        (["memcached"], ImageStyle(symbol: "memorychip.fill", color: rgb(0x4E8A3E))),
        (["node", "bun", "deno"], ImageStyle(symbol: "hexagon.fill", color: rgb(0x5FA04E))),
        (["python"], ImageStyle(symbol: "chevron.left.forwardslash.chevron.right", color: rgb(0x3776AB))),
        (["golang", "go"], ImageStyle(symbol: "g.circle.fill", color: rgb(0x00ADD8))),
        (["openjdk", "eclipse-temurin", "amazoncorretto", "maven", "gradle"], ImageStyle(symbol: "cup.and.saucer.fill", color: rgb(0xE76F00))),
        (["php", "wordpress"], ImageStyle(symbol: "p.circle.fill", color: rgb(0x777BB4))),
        (["ruby", "rails"], ImageStyle(symbol: "diamond.fill", color: rgb(0xCC342D))),
        (["nginx", "httpd", "caddy", "traefik", "haproxy", "envoy"], ImageStyle(symbol: "globe", color: rgb(0x009639))),
        (["rabbitmq"], ImageStyle(symbol: "envelope.fill", color: rgb(0xFF6600))),
        (["kafka", "zookeeper", "nats"], ImageStyle(symbol: "arrow.triangle.branch", color: rgb(0x231F20))),
        (["elasticsearch", "opensearch", "kibana", "meilisearch"], ImageStyle(symbol: "magnifyingglass", color: rgb(0x00BFB3))),
        (["minio", "localstack"], ImageStyle(symbol: "externaldrive.fill", color: rgb(0xC72E49))),
        (["grafana", "prometheus"], ImageStyle(symbol: "chart.xyaxis.line", color: rgb(0xF46800))),
        (["dockiva-gh-runner", "actions-runner", "runner"], ImageStyle(symbol: "gearshape.2.fill", color: rgb(0x24292F))),
        (["alpine", "ubuntu", "debian", "busybox", "fedora", "centos", "archlinux", "shell"], ImageStyle(symbol: "terminal.fill", color: rgb(0x52606D))),
    ]
}
