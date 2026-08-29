package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hausfold/scruff/internal/exitcode"
	"github.com/hausfold/scruff/internal/gitx"
	"github.com/hausfold/scruff/internal/occupancy"
	"github.com/hausfold/scruff/internal/registry"
	"github.com/hausfold/scruff/internal/ui"
)

// The doctor is where scruff inspects its own house. Its first, and for now
// only, patient is the base directory: §8.2 of docs/rename.md — the one step
// in the whole cutover that moves a byte of anyone's work on disk.
// The repo-inspection doctor of SPEC.md §6.4 is a separate, later surface; the
// verb name is reserved here so that surface lands as `scruff doctor`, not as
// a second spelling.
//
// scruff doctor                  report where the base lives and what a move costs
// scruff doctor --migrate-base   do the move (refuses when anything is in it)

// Doctor dispatches the doctor surface.
func (e *Env) Doctor(args []string) error {
	if hasFlag(args, "--migrate-base") {
		return e.migrateBase()
	}
	if firstBare(args) != "" || hasFlag(args, "--write") {
		return exitcode.Usagef("usage: scruff doctor [--migrate-base]\n(no repo inspection yet — see SPEC.md §6.4; only the base report ships today)")
	}
	return e.baseReport()
}

// baseReport tells the operator which base is live, how it resolved, and
// whether the §8.2 move is still pending. A report, never a suggestion:
// the migration itself is explicit.
func (e *Env) baseReport() error {
	ui.Say("base: %s", e.Base)
	switch {
	case os.Getenv("SCRUFF_BASE") != "":
		ui.Say("  resolved from SCRUFF_BASE — the default-path decision is overridden")
	case os.Getenv("CLAUDE_WT_BASE") != "":
		ui.Say("  resolved from CLAUDE_WT_BASE — the default-path decision is overridden")
	default:
		newBase, legacy := defaultBaseCandidates()
		switch e.Base {
		case newBase:
			ui.Say("  the default (~/.cache/scruff)")
		case legacy:
			ui.Say("  the LEGACY path (~/.cache/claude-worktrees) — it holds the registry.")
			ui.Say("  `scruff doctor --migrate-base` moves it to %s", newBase)
		}
	}
	return nil
}

// migrateBase is the base move of docs/rename.md §8.2 — the only scruff
// operation that relocates work on disk, so it is specified rather than
// improvised:
//
//  1. refuse if anything is standing in the base — exit 2, invariant 2 applied
//     to scruff's own migration. "Close your panes and re-run" is a complete
//     answer. Occupancy UNKNOWN is also a refusal: uncertainty resolves to
//     keep, here applied to moving the ground itself.
//  2. hold the registry lock across the whole operation. One lock, one write.
//  3. move the base, then `git worktree repair` every checkout — idempotent,
//     and exactly the operation git ships for this.
//  4. rewrite the registry's paths in the same locked window, a
//     .bak.relocate behind the write.
//  5. leave the old path a symlink to the new base for one release, so any
//     stale absolute path out there still lands somewhere real.
//  6. on failure after the move, put everything back: the failure direction
//     stays "nothing moved", never "half moved".
func (e *Env) migrateBase() error {
	// The migration moves the DEFAULT base. An env override means the
	// operator owns the layout; there is nothing of scruff's to move.
	for _, v := range []string{"SCRUFF_BASE", "CLAUDE_WT_BASE"} {
		if val := os.Getenv(v); val != "" {
			return exitcode.Refusedf("%s=%s is set — the base move is for the default path only; move %s yourself if that is what it points at", v, val, val)
		}
	}

	newBase, legacy := defaultBaseCandidates()
	if _, err := os.Stat(filepath.Join(newBase, "registry.tsv")); err == nil {
		ui.Say("nothing to move — the base is already %s", newBase)
		return nil
	}
	if _, err := os.Stat(filepath.Join(legacy, "registry.tsv")); err != nil {
		return exitcode.Usagef("no registry at %s — nothing to migrate", legacy)
	}
	if e.Base != legacy {
		// Unreachable while baseDir() resolves the default; a guard against a
		// future resolution change moving ground the caller didn't ask about.
		return exitcode.Refusedf("the live base is %s, not %s — refusing to guess", e.Base, legacy)
	}

	// Step 1: occupancy, the whole base at once. Holders() prefix-matches
	// beneath the given path, so one check covers every checkout and any pane
	// idling in the base tree itself.
	occ := e.Occupancy()
	if held := occ.Holders(e.Base); len(held) > 0 {
		return exitcode.Refusedf(
			"refused — something is standing in the base: %s. Nothing moves out from under a live process; close the pane, then re-run",
			occupancy.Describe(e.Base, held))
	}
	if !occ.Known() {
		return exitcode.Refusedf(
			"refused — occupancy could not be determined (no lsof), and a move this size does not guess. " +
				"Install lsof, or set SCRUFF_OCCUPANCY=lease if every session here is one this tool spawned")
	}

	// Step 2: the registry lock, held for everything below.
	unlock, err := e.Reg.HeldLock()
	if err != nil {
		return err
	}
	defer unlock()

	rows, err := e.Reg.Load()
	if err != nil {
		return err
	}

	// The backup, written BEFORE anything moves: the rollback reads it, and it
	// stays afterwards — the record of what the paths were, next to the file
	// it backs up. (It moves with the base and is what step 6 restores.)
	if b, err := os.ReadFile(filepath.Join(legacy, "registry.tsv")); err == nil {
		if err := os.WriteFile(filepath.Join(legacy, "registry.tsv.bak.relocate"), b, 0o644); err != nil {
			return err
		}
	}

	// Step 3a: the move itself.
	if err := os.Rename(legacy, newBase); err != nil {
		return err
	}

	// Step 6's rollback, defined once and used by every step after the move.
	// Order matters: move the tree back FIRST, then re-point the checkouts at
	// the paths that exist again, then restore the registry from the .bak. The
	// whole thing is best effort — if even the rollback fails, say so out loud
	// and keep the cause. The work is intact in the moved tree either way; what
	// must never happen is "half moved", silently.
	rollback := func(cause error) error {
		if err := os.Rename(newBase, legacy); err != nil {
			ui.Warn("could not roll back after %v (%v) — everything is intact under %s, but inspect it by hand", cause, err, newBase)
			return cause
		}
		// repair is idempotent in both directions: the same sweep, told where
		// each checkout lives again.
		for _, row := range rows {
			_, _ = gitx.Run(row.Main, "worktree", "repair", rewritePrefix(newBase, legacy, row.Path))
		}
		if b, err := os.ReadFile(filepath.Join(legacy, "registry.tsv.bak.relocate")); err == nil {
			_ = os.WriteFile(filepath.Join(legacy, "registry.tsv"), b, 0o644)
		}
		ui.Warn("base move rolled back cleanly — everything is at %s as it was", legacy)
		return cause
	}

	// Step 3b: git's own repair, per checkout — repair is told where the
	// checkout lives NOW, because that is what git re-points: the administrative
	// files in the main checkout learn the moved location. A failure here never
	// endangers work — the tree moved intact — it only leaves a lane's link
	// needing a human, so it degrades rather than aborts.
	repaired, linkFailures := 0, 0
	for _, row := range rows {
		if !strings.HasPrefix(row.Path, legacy+"/") {
			continue
		}
		if _, err := gitx.Run(row.Main, "worktree", "repair", rewritePrefix(legacy, newBase, row.Path)); err != nil {
			linkFailures++
			ui.Warn("git worktree repair failed for %s (%v) — the work moved with the tree; the link may need a human", row.Path, err)
			continue
		}
		repaired++
	}

	// Step 4: the registry rewrite, inside the same locked window. The handle
	// from NewEnv points at the OLD path, which moved with the base — the
	// write goes to a handle on the file's NEW location, still under the lock
	// taken at the old one (the lock file moved too; flock travels with the
	// inode, so a mutation racing the move is still excluded).
	for i := range rows {
		rows[i].Path = rewritePrefix(legacy, newBase, rows[i].Path)
		rows[i].Parent = rewritePrefix(legacy, newBase, rows[i].Parent)
	}
	newReg, err := registry.Open(filepath.Join(newBase, "registry.tsv"))
	if err != nil {
		return rollback(fmt.Errorf("opening the registry at its new home: %w", err))
	}
	if err := newReg.WriteAll(rows); err != nil {
		return rollback(fmt.Errorf("the registry rewrite: %w", err))
	}

	// Step 5: the one-release symlink, so stale absolute paths keep landing.
	if err := os.Symlink(newBase, legacy); err != nil {
		return rollback(fmt.Errorf("the legacy-path symlink: %w", err))
	}

	ui.Say("base moved: %s → %s", legacy, newBase)
	ui.Say("  %d checkout(s) re-pointed with git worktree repair", repaired)
	if linkFailures > 0 {
		ui.Say("  %d checkout(s) need their link repaired by hand — the work moved with the tree either way", linkFailures)
		ui.Say("  exit 3: the move completed with %d degraded lane link(s)", linkFailures)
	}
	ui.Say("  %s now points here for one release, so old absolute paths still resolve", legacy)
	if linkFailures > 0 {
		return exitcode.Degradedf("%d lane link(s) need a manual `git worktree repair` — see the warnings above", linkFailures)
	}
	return nil
}

// rewritePrefix re-points one path from under oldBase to newBase. Exact-prefix
// on the path separator only — a base named like a sibling must not match.
func rewritePrefix(oldBase, newBase, p string) string {
	if p == oldBase {
		return newBase
	}
	if strings.HasPrefix(p, oldBase+"/") {
		return newBase + p[len(oldBase):]
	}
	return p
}
