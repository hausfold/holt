// The module path keeps the `nebelhaus` owner ON PURPOSE — it is not a missed
// hit from the 2026-08-09 org move to `hausfold`. `sdk/go` is published on
// Go's immutable proxy at this path, the root module is a binary so its path
// is nobody's API, and changing it is a 60-import sweep coupled to a five-SDK
// version contract. Decided in the rename plan's §6; the GitHub redirect from
// the (deliberately never deleted) old org is what keeps `go get` resolving.
module github.com/nebelhaus/holt

go 1.26

// The dependency-free run ends here, deliberately: `holt watch --json`
// (SPEC.md §14.3 step 2) needs a real filesystem-event API, and every
// alternative to a well-worn cross-platform library is reinventing kqueue and
// inotify by hand for a lifecycle stream that other people's SDKs depend on.
// fsnotify is the one dependency this crosses the threshold for. The UX layer
// (fang/lipgloss) and the TOML adapter parser still wait for 0.2.
require github.com/fsnotify/fsnotify v1.7.0

require golang.org/x/sys v0.4.0 // indirect
