// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Scruff",
    // No iOS/tvOS/watchOS: `Foundation.Process` cannot spawn a subprocess on
    // those platforms (App Store sandboxing forbids it outright), and there
    // is no `scruff` binary to bundle even if it could. macOS covers the TUI/
    // native-chat-app case; Linux (unlisted here — SwiftPM's `platforms`
    // only constrains Apple OSes) covers the server/orchestrator case.
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "Scruff", targets: ["Scruff"]),
    ],
    targets: [
        .target(name: "Scruff"),
        .testTarget(name: "ScruffTests", dependencies: ["Scruff"], resources: [.copy("fake-scruff.sh")]),
    ]
)
