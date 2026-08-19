package commands

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hausfold/holt/internal/gitx"
	"github.com/hausfold/holt/internal/registry"
)

type sweepMode int

const (
	// sweepParked touches only lanes with no checkout on disk. Nothing a pane
	// could be sitting in is at risk, which is why the listing runs it.
	sweepParked sweepMode = iota
	// sweepAll additionally considers live checkouts — clean, landed and
	// unoccupied ones only. Opt-in, via `holt reap`.
	sweepAll
)

// SweepResult is what one sweep did, and what it deliberately did not do.
type SweepResult struct {
	Reaped      []string
	Strays      []string
	SkippedLive []string
	Dirty       []string // reapable but for uncommitted work in the checkout
	Relanded    []string // landed PR, but the branch committed past it
	Diverged    []string // landed PR, but the tip isn't built on what merged
	DeadEnds    []string // nothing will ever land this: PR closed, or repo archived
	Degraded    bool     // occupancy was unknowable, so live checkouts were spared
}

// reapSweep removes every LANDED lane the mode allows, and nothing else.
//
// Every `continue` in here is a safety invariant, not an optimisation. The
// failure direction is always "a branch lingers": a branch that outlives its
// usefulness is a nuisance, a branch reaped with work still on it is the thing
// holt exists to never do.
//
// What makes a lane reapable is not yet a policy seam (SPEC.md §6.5). It is
// the one decision here that reaches through THREE of holt's inherited
// opinions at once — occupancy, dirtiness and landedness — and a seam over the
// lot of them has to wait for the shape those settle into.
func (e *Env) reapSweep(mode sweepMode) SweepResult {
	var res SweepResult
	occ := e.Occupancy()
	if !occ.Known() && mode == sweepAll {
		// "Landed and clean" does NOT mean "nobody is standing here". Without a
		// way to ask, the live half of the sweep is unsafe, so degrade to
		// parked-only rather than guess.
		mode = sweepParked
		res.Degraded = true
		// "No provider vouched for absence", which on a developer machine means
		// no lsof. Leases alone never reach this branch: they assert presence
		// only, so a lane nobody leased is still an open question. An embedder
		// that owns every session it serves says so with HOLT_OCCUPANCY=lease.
		e.Warn("no lsof — can't tell which checkouts have a pane open, so only PARKED lanes were swept (an embedder that owns every session can answer with HOLT_OCCUPANCY=lease)")
	}
	selfTop, _ := gitx.Toplevel(e.Cwd)

	for _, entry := range e.discover() {
		switch entry.State {
		case Stray:
			// A husk: the contents are preserved but git has disowned it.
			// Reported, never swept — `holt <name>` moves it aside and rebuilds.
			res.Strays = append(res.Strays,
				entry.Name()+" ("+filepath.Base(entry.Main)+") → "+entry.Path)
			continue

		case Live:
			if mode != sweepAll {
				continue
			}
			if entry.Path == selfTop {
				continue // never the checkout we are being run from
			}
			if occ.Occupied(entry.Path) {
				// Landed or not, a pane is standing in it. Removing the checkout
				// yanks the cwd out from under a running client: the shell and
				// the agent keep running in a deleted directory and every
				// subsequent tool call fails.
				res.SkippedLive = append(res.SkippedLive,
					entry.Name()+" ("+filepath.Base(entry.Main)+")")
				continue
			}
			dirt, err := gitx.Status(entry.Path)
			if err != nil {
				// git could not answer, so we do not know the tree is clean —
				// and this is the branch that DELETES. Uncertainty resolves to
				// keep, out loud.
				res.Dirty = append(res.Dirty, entry.Name()+" ("+filepath.Base(entry.Main)+")"+
					" — git could not read the checkout, so holt cannot tell whether"+
					" there is unsaved work in it; nothing is reaped on a guess: "+entry.Path)
				continue
			}
			if dirt != "" {
				// Say so. Every other refusal in this loop leaves a note, and
				// this one used to `continue` in silence — so a landed,
				// unoccupied lane held back by one stray untracked file read as
				// a lane holt had simply forgotten, with the summary line's
				// "unmerged, dirty, or in use" left to guess between.
				res.Dirty = append(res.Dirty, dirtyNote(entry, dirt))
				continue // uncommitted work — leave it for a human
			}
			if !e.Landed(entry.Main, entry.Branch).Landed {
				e.noteRelanded(&res, entry)
				continue
			}
			if _, err := gitx.Run(entry.Main, "worktree", "remove", entry.Path); err != nil {
				continue // free the branch first, or don't touch the branch
			}
		}

		if e.reapBranch(entry.Main, entry.Branch) {
			_ = e.Reg.Delete(entry.Path)
			res.Reaped = append(res.Reaped, entry.Name()+" ("+filepath.Base(entry.Main)+")")
		} else {
			e.noteRelanded(&res, entry)
		}
	}
	e.pruneRegistry()
	return res
}

// dirtyNote names what is actually in the way, because "dirty" alone sends you
// to `git status` in a checkout you have to find first — and the paths are
// usually the whole answer (the case that prompted this was one untracked
// directory a tool had dropped there, obvious junk the moment it was named).
//
// Deliberately NOT "holt park it": park commits the tree as a `wip:` commit,
// which makes the branch unlanded and moves the lane from this refusal to the
// next one. The two real ways out are commit it or clean it.
func dirtyNote(entry Entry, porcelain string) string {
	lines := strings.Split(strings.TrimRight(porcelain, "\n"), "\n")
	shown := lines
	if len(shown) > dirtyPathsShown {
		shown = shown[:dirtyPathsShown]
	}
	paths := make([]string, 0, len(shown))
	for _, l := range shown {
		// Porcelain v1 is "XY path"; the status letters are always 2 columns
		// plus a space, and a rename's " -> " tail is worth keeping as written.
		if len(l) > 3 {
			l = l[3:]
		}
		// Porcelain C-quotes any path with non-ASCII or special characters
		// (`?? "caf\303\251.txt"`). Trimming the quotes alone leaves the
		// escapes in, which is not a path anyone can paste back.
		if unq, err := strconv.Unquote(l); err == nil {
			l = unq
		}
		paths = append(paths, l)
	}
	more := ""
	if len(lines) > len(shown) {
		more = ", +" + itoa(len(lines)-len(shown)) + " more"
	}
	return entry.Name() + " (" + filepath.Base(entry.Main) + ")" +
		" — uncommitted work in the checkout: " + strings.Join(paths, ", ") + more +
		". Nothing is reaped over that; commit it or clean it, then reap again: " + entry.Path
}

// dirtyPathsShown caps the named paths, so a checkout with a build tree in it
// reports a line rather than a screenful.
const dirtyPathsShown = 3

// noteRelanded records why a lane declined to be reaped, so it says something
// instead of silently persisting — and points at the right fix. Three shapes,
// mutually exclusive, each with its own remedy:
//
//   - "moved on" — real work after the merge → `holt reship`.
//   - "diverged" — a stale or sideways tip that never built on what merged (a
//     second checkout of the same branch that pushed first, a rebase, an amend).
//     Same nonzero commit count as "moved on" and the opposite remedy: its
//     content already landed, so reshipping would push and PR what the merge
//     already superseded. Remove the checkout instead.
//   - "dead end" — no merged PR at all, and there never will be one, because the
//     PR was closed unmerged or the repo is archived → `holt drop`.
//
// The dead-end question is asked LAST and only when the count is zero, so its
// two forge calls stay off the path every healthy lane walks.
func (e *Env) noteRelanded(res *SweepResult, entry Entry) {
	name := entry.Name() + " (" + filepath.Base(entry.Main) + ")"
	n, pr, diverged := e.postMergeAhead(entry.Main, entry.Branch)
	if n == 0 {
		// `reap` still won't touch a dead end — the commits are unlanded and this
		// sweep is automatic — but a lane that can never land has to SAY so, or it
		// reads exactly like one still in flight and outlives everything around it.
		if why := e.deadEnd(entry.Main, entry.Branch); why != "" {
			res.DeadEnds = append(res.DeadEnds,
				name+" — "+why+": rescue the commits, or holt drop "+entry.Name())
		}
		return
	}
	if diverged {
		res.Diverged = append(res.Diverged,
			name+" — merged PR #"+itoa(pr)+", but the tip isn't built on what merged."+
				" Its content already landed; remove the checkout instead of reshipping it.")
		return
	}
	res.Relanded = append(res.Relanded,
		name+" — merged PR #"+itoa(pr)+
			", "+itoa(n)+" commit(s) since, covered by no PR: holt reship "+entry.Name())
}

// reapBranch deletes a branch, and ONLY once it has provably landed.
//
// `git branch -d` is not the gate: it measures against the checkout's current
// HEAD, so it happily deletes a branch merged only into whatever side branch the
// main checkout is parked on. Landed measures against the repo's DEFAULT branch
// (and the branch's merged PR for squash merges), which is the question we
// actually mean. Having confirmed it, -d/-D is just the mechanism: -d first so
// git's own safety net still gets a say, -D for the squash case it cannot see.
func (e *Env) reapBranch(main, branch string) bool {
	v := e.Landed(main, branch)
	if !v.Landed {
		return false
	}
	// The ledger is written BEFORE the delete and only here, because this is the
	// single choke point every deletion goes through — the listing's parked
	// sweep, `holt reap`, and the remove hook all arrive at this function. The
	// SHA is only resolvable while the branch still exists, and it is the whole
	// point: `git branch -D` takes the branch's reflog with it, so without this
	// line a lane that went away is unrecoverable AND unexplainable.
	e.noteReaped(main, branch, v)
	if gitx.OK(main, "branch", "-d", branch) {
		return true
	}
	return gitx.OK(main, "branch", "-D", branch)
}

// pruneRegistry drops rows whose branch no longer means anything.
func (e *Env) pruneRegistry() {
	_ = e.Reg.Prune(func(row registry.Row) bool {
		return e.branchAlive(Entry{
			Main:   row.Main,
			Branch: row.Branch,
			Path:   row.Path,
			State:  checkoutState(row.Path),
		})
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
