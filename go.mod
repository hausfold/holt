// ⚠️ This path MOVED on 2026-08-16, and a Go module path is the one kind of
// name that cannot be moved cleanly: `sdk/go` is published on Go's immutable
// proxy, so every version up to v0.2.8 stays resolvable under the old owner
// forever and nothing published from here on is reachable there. An existing
// importer edits its import line or stays on v0.2.8. The root module is a
// binary, so its own path is nobody's API.
module github.com/hausfold/holt

go 1.26

// The dependency-free run ends here, deliberately: `holt watch --json`
// (SPEC.md §14.3 step 2) needs a real filesystem-event API, and every
// alternative to a well-worn cross-platform library is reinventing kqueue and
// inotify by hand for a lifecycle stream that other people's SDKs depend on.
// fsnotify is the one dependency this crosses the threshold for. The UX layer
// (fang/lipgloss) and the TOML adapter parser still wait for 0.2.
require github.com/fsnotify/fsnotify v1.7.0

require golang.org/x/sys v0.4.0 // indirect
