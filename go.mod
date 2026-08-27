// ⚠️ This path MOVED on 2026-08-16, and a Go module path is the one kind of
// name that cannot be moved cleanly: `sdk/go` is published on Go's immutable
// proxy, so every version up to v0.2.8 stays resolvable under the old owner
// forever and nothing published from here on is reachable there. An existing
// importer edits its import line or stays on v0.2.8. The root module is a
// binary, so its own path is nobody's API.
module github.com/hausfold/holt

go 1.26

// The dependency-free run ended here, deliberately: `holt watch --json`
// (SPEC.md §14.3 step 2) needs a real filesystem-event API, and every
// alternative to a well-worn cross-platform library is reinventing kqueue and
// inotify by hand for a lifecycle stream that other people's SDKs depend on.
// fsnotify is the one dependency that crossed the threshold first. The TOML
// adapter parser still waits.
require github.com/fsnotify/fsnotify v1.7.0

// The UX layer this file said would wait for 0.2, arriving as snug rather than
// as the fang/lipgloss it guessed at — and the difference is the reason it is
// here at all.
//
// `internal/ui` carried three xterm-256 indices copied out of the bash `wt`
// during the cutover. Measured against nebelung they sat ΔE 21.8 / 22.3 / 27.4
// from the tokens they were meant to be, and `say` resolved to BLUE, the one hue
// nebelung exists to strip out. Three constants nobody could see were wrong by
// reading them is not a thing to keep hand-maintaining, and the fix is not a
// better set of three constants — it is naming ROLES and letting one library own
// the resolution, the degradation by terminal capability, and the width.
//
// The cost is nine modules, and it is worth stating plainly because that number
// is the whole argument this comment used to make:
//
//   - snug itself, plus x/ansi and x/term, plus their six transitive deps.
//   - snug is charm's ANSI-aware string layer WITHOUT lipgloss's styling engine
//     — 9 modules against lipgloss v2's 22, 2.1 MB against 3.0. It is the
//     cheaper half of the very thing this comment was holding out against.
//   - It reaches NOTHING a downstream inherits. `sdk/go` is its own module with
//     its own go.mod, so the five published SDKs' dependency graphs are
//     untouched; this binds the binary alone. That is what made it affordable.
//   - It holds the same stdout-is-data-only contract holt already documents in
//     SPEC.md §2.3, from its own side: Say/Warn/Fail write to stderr and Data is
//     the only thing that reaches stdout.
//
// Pinned to a commit rather than a tag because snug has cut none yet. Re-pin to
// the first tag when there is one.
require github.com/hausfold/snug v0.0.0-20260827094928-105351ab4f3c

require (
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)
