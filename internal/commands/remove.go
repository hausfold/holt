package commands

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/ui"
)

// HookRemove implements the WorktreeRemove hook: retire a worktree WITHOUT
// losing work.
//
// A plain `git worktree remove --force` silently discards uncommitted edits.
// Committed work always survives on the branch; the dirty remainder is parked
// there too, as the same wip commit `holt park` makes by hand — so closing a
// pane can never cost you work.
func (e *Env) HookRemove(stdin io.Reader) error {
	payload, err := readHookPayload(stdin)
	if err != nil {
		return err
	}
	dir, _ := hookField(payload, "worktree_path", "path")
	if dir == "" {
		return exitcode.Usagef("the hook payload has none of the keys I wanted (worktree_path, path)")
	}
	main, err := gitx.MainCheckout(dir)
	if err != nil {
		return exitcode.Usagef("worktree %q isn't a git checkout — nothing to retire", dir)
	}
	branch := gitx.CurrentBranch(dir)

	// preserved means nothing on disk is irreplaceable any more: either the tree
	// was clean, or the wip commit captured it, or it was landed-plus-untracked
	// scratch we chose to drop. It gates the husk cleanup below, and nothing else.
	preserved := true
	if porcelain := gitx.Porcelain(dir); porcelain != "" {
		if e.mustPreserve(main, branch, porcelain) {
			if err := wipCommit(dir, "wip: auto-saved on pane close ("+time.Now().Format("2006-01-02 15:04")+")"); err != nil {
				preserved = false
			}
		}
	}

	if _, err := gitx.Run(main, "worktree", "remove", dir); err != nil {
		_, _ = gitx.Run(main, "worktree", "remove", "--force", dir)
	}

	// Finish what git started. `git worktree remove` deletes the admin dir
	// BEFORE the working tree, so a recursive delete that fails part-way — most
	// often an ignored build dir it cannot unlink — leaves a husk. Nothing
	// recovers on its own: it sat in the base directory reading `live` forever,
	// and it killed the statusline refresher outright.
	//
	// If the wip commit FAILED, that residue is the only copy of those edits —
	// leave it and say so. A husk that lingers is a nuisance; a husk deleted with
	// the work still in it is the thing holt exists to never do.
	if _, err := os.Stat(dir); err == nil {
		switch checkoutState(dir) {
		case Stray:
			if preserved {
				// Best-effort: whatever defeated git's delete can defeat ours
				// too. Never fatal — a husk holt names and heals is a nuisance,
				// while a hook that dies here leaves the branch unreaped and the
				// registry stale.
				_ = os.RemoveAll(dir)
				if _, err := os.Stat(dir); err == nil {
					ui.Say("git left a partly-removed checkout at %s and we couldn't finish it either — `holt` lists it as stray", dir)
				}
			} else {
				ui.Say("couldn't save this worktree's edits AND git couldn't remove it — left at %s", dir)
			}
		case Live:
			// Even --force refused, and git never got as far as unregistering.
			// The branch is still checked out here, so don't reap it out from
			// under the checkout.
			ui.Say("git wouldn't remove %s — the worktree is still registered; try: holt reap", dir)
			return nil
		}
	}

	// The branch is how unmerged work survives; only reap it once landed. Keep
	// the registry row in lockstep: gone when reaped, kept while resumable.
	if branch != "" && e.reapBranch(main, branch) {
		_ = e.Reg.Delete(dir)
	}
	return nil
}

// mustPreserve decides whether a dirty tree needs a wip commit before removal.
//
// One exception, and it matters: a branch whose PR has ALREADY merged, whose
// only remaining changes are UNTRACKED files, is holding build scratch (a
// target/, a .venv/) — not history. Wip-committing it moves the tip one commit
// past the merged PR's SHA, so the branch no longer matches its merge and the
// worktree gets falsely PARKED instead of reaped. That is how merged worktrees
// piled up. Tracked edits, or an unmerged branch, are real work → always kept.
func (e *Env) mustPreserve(main, branch, porcelain string) bool {
	if branch == "" {
		return true
	}
	for _, line := range gitx.Lines(porcelain) {
		if !strings.HasPrefix(line, "??") {
			return true // a tracked edit — always preserve
		}
	}
	return !e.Landed(main, branch).Landed
}
