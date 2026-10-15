// swift-tools-version:6.0
import PackageDescription

// Dockiva's native macOS front end. It runs the Go core (`dockiva --core SOCKET`)
// and talks to it over a unix socket; see core_api.go.
let package = Package(
    name: "Dockiva",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "Dockiva", path: "Sources/Dockiva", swiftSettings: [.swiftLanguageMode(.v5)]),
        .testTarget(name: "DockivaTests", dependencies: ["Dockiva"], path: "Tests/DockivaTests", swiftSettings: [.swiftLanguageMode(.v5)]),
    ]
)
