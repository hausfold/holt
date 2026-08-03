package commands

import (
	"strings"
	"time"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/ui"
)

// Park sets the working tree aside as one wip: commit on the current branch.
//
// Why this exists at all: `git stash` looks per-worktree and isn't. The stash
// stack lives in the shared .git dir, so every worktree of a repo — and the main
// checkout — push and pop ONE stack. Two parallel agents stashing means either
// can pop the other's entry, and the loser's edits land in a tree that never
// asked for them. A wip commit has no such stack: it sits on the branch only
// this pane has checked out, survives a pane close, and `holt unpark` puts it
// back.
func (e *Env) Park(label string) error {
	top, err := gitx.Toplevel(e.Cwd)
	if err != nil {
		return exitcode.Usagef("not in a git repo — nothing to park")
	}
	branch := gitx.CurrentBranch(top)
	// Detached HEAD is the one place this would recreate stash's failure mode:
	// the commit is reachable from nothing, so the next checkout orphans it.
	if branch == "" {
		return exitcode.Refusedf("HEAD is detached — a parked commit here would be unreachable. Check out a branch first.")
	}
	dirty := gitx.Porcelain(top)
	if dirty == "" {
		ui.Say("nothing to park — %s is already clean.", branch)
		return nil
	}

	stamp := time.Now().Format("2006-01-02 15:04")
	msg := "wip: parked " + stamp
	if label != "" {
		msg = "wip: " + label + " (parked " + stamp + ")"
	}
	if err := wipCommit(top, msg); err != nil {
		return exitcode.Usagef("commit failed — nothing was parked; `git -C %s status` will say why.", top)
	}

	ui.Say("parked %d change(s) on %s → %s", len(gitx.Lines(dirty)), branch, gitx.ShortRev(top, "HEAD"))
	if !strings.HasPrefix(branch, "worktree-") {
		ui.Say("note: '%s' isn't an agent branch — don't push this wip commit.", branch)
	}
	ui.Say("bring them back with: holt unpark")
	return nil
}

// wipCommit stages everything, untracked included, and commits it. Sweeping in
// untracked files is the point of "set the tree aside" — a half-written new file
// is exactly the work a pane close would otherwise lose.
func wipCommit(top, msg string) error {
	if _, err := gitx.Run(top, "add", "-A"); err != nil {
		return err
	}
	_, err := gitx.Run(top, "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
	return err
}

// Unpark rewinds the last wip: commit, putting those changes back in the
// working tree, uncommitted — the `git stash pop` half.
func (e *Env) Unpark() error {
	top, err := gitx.Toplevel(e.Cwd)
	if err != nil {
		return exitcode.Usagef("not in a git repo — nothing to unpark")
	}
	if gitx.CurrentBranch(top) == "" {
		return exitcode.Refusedf("HEAD is detached — check out a branch first.")
	}
	subject := gitx.Subject(top, "HEAD")
	if !strings.HasPrefix(subject, "wip:") {
		return exitcode.Refusedf("HEAD isn't a parked commit (it's %q) — nothing to unpark.", subject)
	}
	if gitx.Rev(top, "HEAD^") == "" {
		return exitcode.Refusedf("that wip commit is the branch's first commit — there's nothing to rewind onto.")
	}
	// Refuse to rewrite anything already published. A parked commit that got
	// pushed is visible in an open PR, so rewinding it locally turns "give me my
	// files back" into a force-push — never do that behind the user's back.
	if gitx.PushedAnywhere(top, "HEAD") {
		return exitcode.Refusedf("that wip commit is already pushed — unparking would rewrite published history. If you mean it: git reset --mixed HEAD^")
	}
	// --mixed, not --hard: the files stay on disk exactly as parked and go back
	// to being uncommitted (staged adds become untracked again), which is what
	// pop does.
	if _, err := gitx.Run(top, "reset", "-q", "--mixed", "HEAD^"); err != nil {
		return exitcode.Usagef("reset failed — the parked commit is untouched.")
	}
	ui.Say("unparked %q on %s — those changes are back in the working tree, uncommitted.",
		subject, gitx.CurrentBranch(top))
	return nil
}
