// ⚠️ This path has MOVED TWICE, and a Go module path is the one kind of name
// that cannot be moved cleanly: `sdk/go` is published on Go's immutable proxy,
// so each old path stays resolvable at the versions published under it forever
// and nothing published after a move is reachable there.
//
//	≤ v0.2.8         github.com/nebelhaus/holt   (the org rename, 2026-08-16)
//	v0.2.9 … v0.5.0  github.com/hausfold/holt    (the scruff rename, 1.0.0)
//	≥ v1.0.0         github.com/hausfold/scruff
//
// An importer on either old path edits its import line or pins the last tag
// published under it. The root module is a binary, so its own path is nobody's
// API.
module github.com/hausfold/scruff

go 1.26

// The dependency-free run ended here, deliberately: `scruff watch --json`
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
//   - It holds the same stdout-is-data-only contract scruff already documents in
//     SPEC.md §2.3, from its own side: Say/Warn/Fail write to stderr and Data is
//     the only thing that reaches stdout.
//
// The listing is snug's too, not just the diagnostics: `snug.Table` budgets the
// columns against the real window, which is what let `renderTable` stop
// measuring in bytes over a width `tput cols` had guessed at.
//
// Pinned to a commit rather than a tag because snug has cut none yet — this one
// is main's tip, the merge of hausfold/snug#12. Re-pin to the first tag when
// there is one, and note that `go get snug@main` can answer with a STALE commit
// for a while after a merge: the proxy caches the branch resolution, and asking
// for @main minutes after #1 landed downgraded this line to the commit before
// the fix. Ask for the SHA when it matters.
//
// Which commit is not a housekeeping detail: THE MARKS ARE SNUG'S. `MarkSay`
// was the fog emoji (U+1F32B) and is now `≋` — snug 90bd364, "the signature
// mark is the ascii tilde, tripled — no emoji in the table". haus and bench had
// already moved; this line was the last thing in the family still drawing fog,
// and grepping scruff's tree for the glyph found nothing, because there is
// nothing to find. A mark scruff prints changes HERE.
//
// Moving this line moves `vendorHash` in flake.nix too — see the comment there.
require github.com/hausfold/snug v0.0.0-20260831060937-a6d221002232

require (
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)
