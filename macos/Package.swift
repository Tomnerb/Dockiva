// swift-tools-version:5.9
import PackageDescription

// Dockiva's native macOS front end. It runs the Go core (`dockiva --core SOCKET`)
// and talks to it over a unix socket; see core_api.go.
let package = Package(
    name: "Dockiva",
    platforms: [.macOS(.v14)],
    dependencies: [
        // Exact: 1.18.0 is the last release whose manifest is swift-tools-version 5.9 (1.19+ need 6.0).
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.18.0"),
    ],
    targets: [
        .executableTarget(name: "Dockiva", dependencies: [.product(name: "SwiftTerm", package: "SwiftTerm")], path: "Sources/Dockiva"),
        .testTarget(name: "DockivaTests", dependencies: ["Dockiva"], path: "Tests/DockivaTests"),
    ]
)
