// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Holt",
    // No iOS/tvOS/watchOS: `Foundation.Process` cannot spawn a subprocess on
    // those platforms (App Store sandboxing forbids it outright), and there
    // is no `holt` binary to bundle even if it could. macOS covers the TUI/
    // native-chat-app case; Linux (unlisted here — SwiftPM's `platforms`
    // only constrains Apple OSes) covers the server/orchestrator case.
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "Holt", targets: ["Holt"]),
    ],
    targets: [
        .target(name: "Holt"),
        .testTarget(name: "HoltTests", dependencies: ["Holt"], resources: [.copy("fake-holt.sh")]),
    ]
)
