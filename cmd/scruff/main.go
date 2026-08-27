// Command scruff manages LANES — one coding agent's branch, checkout and pane —
// for any git repo.
//
// See SPEC.md for the design. The short version: scruff owns a lane's LIFECYCLE
// (create → live → parked → landed → reaped) and its safety invariants — never
// lose work, never reap something in use, keep the registry locked. What you do
// at each transition is yours.
//
// "lane" is deliberate, and the vocabulary is closed: `worktree` is git's
// checkout (a parked lane has none), `agent` is the CLIENT a lane runs, and
// `session` belongs to the multiplexer and to the clients. See SPEC.md §0.
//
// This tool was called `holt` until 1.0.0 and still answers to it — the old
// name is a symlink onto this same binary, and it is deleted at 1.1.0. The
// whole cutover is docs/rename.md.
package main

import (
	"os"

	"github.com/hausfold/scruff/internal/commands"
	"github.com/hausfold/scruff/internal/compat"
	"github.com/hausfold/scruff/internal/exitcode"
	"github.com/hausfold/scruff/internal/ui"
)

func main() {
	noticeIfOldName()
	err := commands.Run(os.Args[1:])
	if err != nil {
		ui.Fail(err.Error())
	}
	os.Exit(exitcode.Of(err))
}

// noticeIfOldName tells a HUMAN, once per invocation, that `holt` is now
// `scruff` — and tells nobody else.
//
// Gated on stderr being a terminal, which is the difference between a rename
// notice and a rename that breaks things. Every non-interactive caller of this
// binary is one that matters: Claude Code's WorktreeCreate/Remove and
// Notification hooks, sketchybar's bar plugins polling several times a minute,
// the acceptance suite asserting on stderr, and any script parsing --json. A
// line printed into those is noise at best and a failed assertion at worst,
// and none of them is the audience — the person typing the old name is.
func noticeIfOldName() {
	if !compat.InvokedByOldName(os.Args[0]) || !ui.IsTTY(os.Stderr) {
		return
	}
	ui.Warn("`holt` is now `scruff` — same tool, same flags. The old name is removed at 1.1.0.")
}
