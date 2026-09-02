// Package scruff carries the one thing scruff ships that is neither code nor a
// checkout: its agent skills, compiled into the binary.
//
// It sits at the repo ROOT for one mechanical reason — a `//go:embed` pattern
// cannot escape its own package directory, and `ai/` is at the root because the
// family standard (the workshop's `docs/agent-surface.md` §A4) puts it there.
// The alternative is a second copy of the prose under `internal/`, which is the
// exact thing script/check-skills.sh's discover-don't-list design exists to
// prevent: two copies is one place to forget an edit.
//
// go.mod's warning still stands — the root module path is nobody's API. This is
// an implementation detail of the binary that happens to be importable, not a
// library surface, and `internal/commands` is its only consumer.
package scruff

import "embed"

// Skills is the `ai/` tree as it was at BUILD time: `ai/SKILL.md` is scruff's
// own, and every `ai/<name>/SKILL.md` beside it is a sibling skill.
//
// Embedded rather than read off disk because the three ways scruff reaches a
// machine put its files in three different places — a Nix store path, a
// `go install` binary with no repo anywhere near it, a Homebrew cellar — and
// only embedding makes the version that answers `--help` the version that
// answers `scruff skill` (`docs/agent-surface.md` §A3).
//
// Nothing here is enumerated: `internal/commands`'s skillDocs walks this tree,
// so a third skill is a new directory under `ai/` and no edit anywhere else.
//
//go:embed ai
var Skills embed.FS
