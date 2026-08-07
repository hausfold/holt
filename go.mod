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
