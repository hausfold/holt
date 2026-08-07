package commands

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nebelhaus/holt/internal/config"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/registry"
)

type sweepMode int

const (
	// sweepParked touches only worktrees with no checkout on disk. Nothing a
	// pane could be sitting in is at risk, which is why the listing runs it.
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
	Relanded    []string // landed PR, but the branch committed past it
	Declined    []string // the `reapable` hook said no, in its own words
	Degraded    bool     // occupancy was unknowable, so live checkouts were spared
}

// reapSweep removes every REAPABLE worktree the mode allows, and nothing else.
//
// Every `continue` in here is a safety invariant, not an optimisation. The
// failure direction is always "a branch lingers": a branch that outlives its
// usefulness is a nuisance, a branch reaped with work still on it is the thing
// holt exists to never do.
//
// "Reapable" is a policy, and the `reapable` hook owns it whole when it is
// configured — occupancy and dirtiness included, not just the landed rung.
// That breadth is the point: a machine that can enumerate its own panes knows
// better than an lsof heuristic, and a repo whose worktrees are disposable may
// not care about a dirty tree at all. Two floors survive any answer, because
// they are about holt not sawing off the branch it is sitting on rather than
// about policy:
//
//   - the checkout holt is being run FROM is never swept;
//   - a stray is never swept, only reported.
func (e *Env) reapSweep(mode sweepMode) SweepResult {
	var res SweepResult
	occupied, occKnown := occupancy()
	if !occKnown && mode == sweepAll && !e.Cfg.Defined(config.HookReapable) {
		// "Landed and clean" does NOT mean "nobody is standing here". Without a
		// way to ask, the live half of the sweep is unsafe, so degrade to
		// parked-only rather than guess. A `reapable` hook is exactly the
		// machine saying it can answer this itself, so it is not degraded by
		// holt's own occupancy probe going missing.
		mode = sweepParked
		res.Degraded = true
		e.Warn("no lsof — can't tell which checkouts have a pane open, so only PARKED worktrees were swept")
	}
	selfTop, _ := gitx.Toplevel(e.Cwd)

	for _, entry := range e.discover() {
		if entry.State == Stray {
			// A husk: the contents are preserved but git has disowned it.
			// Reported, never swept — `holt <name>` moves it aside and rebuilds.
			res.Strays = append(res.Strays,
				entry.Name()+" ("+filepath.Base(entry.Main)+") → "+entry.Path)
			continue
		}
		if entry.State == Live && mode != sweepAll {
			continue
		}
		if entry.Path == selfTop {
			continue // never the checkout we are being run from
		}

		hook := e.askReapable(entry, occupied, occKnown)
		if hook.Answer == config.No {
			why, _ := hook.Data["why"].(string)
			if why == "" {
				why = "the reapable hook said no"
			}
			res.Declined = append(res.Declined,
				entry.Name()+" ("+filepath.Base(entry.Main)+") — "+why)
			continue
		}

		if entry.State == Live {
			if hook.Answer == config.Defer {
				if isOccupied(occupied, entry.Path) {
					// Landed or not, a pane is standing in it. Removing the
					// checkout yanks the cwd out from under a running session:
					// the shell and the agent keep running in a deleted
					// directory and every subsequent tool call fails.
					res.SkippedLive = append(res.SkippedLive,
						entry.Name()+" ("+filepath.Base(entry.Main)+")")
					continue
				}
				if gitx.Dirty(entry.Path) {
					continue // uncommitted work — leave it for a human
				}
				if !e.Landed(entry.Main, entry.Branch).Landed {
					e.noteRelanded(&res, entry)
					continue
				}
			}
			if _, err := gitx.Run(entry.Main, "worktree", "remove", entry.Path); err != nil {
				// git refuses to remove a checkout with uncommitted changes,
				// which is holt's own rule enforced one layer down — and when
				// the hook cleared this worktree, that rule is the one being
				// overridden. The hook was handed `dirty` in its payload and
				// answered yes anyway, so this is a decision it made with the
				// fact in hand, not one holt inferred. It is still named in the
				// output: work that goes away should always leave a sentence
				// behind saying who said it could.
				if hook.Answer != config.Yes {
					continue // free the branch first, or don't touch the branch
				}
				if _, ferr := gitx.Run(entry.Main, "worktree", "remove", "--force", entry.Path); ferr != nil {
					continue
				}
				e.Warn("the reapable hook cleared " + entry.Name() + " while it had uncommitted changes — they are gone")
			}
		}

		if e.reapBranch(entry.Main, entry.Branch, hook.Answer == config.Yes) {
			_ = e.Reg.Delete(entry.Path)
			res.Reaped = append(res.Reaped, entry.Name()+" ("+filepath.Base(entry.Main)+")")
		} else {
			e.noteRelanded(&res, entry)
		}
	}
	e.pruneRegistry()
	return res
}

// askReapable puts the whole reapability question to the hook, with holt's own
// findings attached so the hook can lean on them rather than re-derive them.
//
// `occupied` is the string "true"/"false"/"unknown": a hook must be able to
// tell "no pane is here" from "holt could not find out", because those two
// justify very different answers.
func (e *Env) askReapable(entry Entry, occupied []string, occKnown bool) config.Result {
	if !e.Cfg.Defined(config.HookReapable) {
		return config.Result{Answer: config.Defer}
	}
	payload := e.hookPayload(entry.Main, entry.Branch, entry.Path, e.agentForPath(entry.Path))
	payload["state"] = string(entry.State)
	payload["occupied"] = "unknown"
	if occKnown {
		payload["occupied"] = boolString(isOccupied(occupied, entry.Path))
	}
	payload["dirty"] = "false"
	if entry.State == Live {
		payload["dirty"] = boolString(gitx.Dirty(entry.Path))
	}
	payload["landed"] = boolString(e.Landed(entry.Main, entry.Branch).Landed)

	res := e.Cfg.Ask(config.HookReapable, payload)
	e.noteHook(res)
	return res
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// noteRelanded records the "its PR merged but the branch moved on" case, so a
// worktree that declines to be reaped says why instead of silently persisting.
func (e *Env) noteRelanded(res *SweepResult, entry Entry) {
	if n, pr := e.postMergeAhead(entry.Main, entry.Branch); n > 0 {
		res.Relanded = append(res.Relanded,
			entry.Name()+" ("+filepath.Base(entry.Main)+") — merged PR #"+itoa(pr)+
				", "+itoa(n)+" commit(s) since, covered by no PR: holt reship "+entry.Name())
	}
}

// reapBranch deletes a branch, and ONLY once it has provably landed — or once
// the `reapable` hook has taken that decision on itself (`cleared`).
//
// `git branch -d` is not the gate: it measures against the checkout's current
// HEAD, so it happily deletes a branch merged only into whatever side branch the
// main checkout is parked on. Landed measures against the repo's DEFAULT branch
// (and the branch's merged PR for squash merges), which is the question we
// actually mean. Having confirmed it, -d/-D is just the mechanism: -d first so
// git's own safety net still gets a say, -D for the squash case it cannot see.
//
// `cleared` exists so the checkout and the branch can never disagree: a hook
// that authorised sweeping this worktree has already had the checkout removed
// on its say-so, and re-litigating the branch against holt's own landed rule
// would strand a branch with no checkout on a machine that deliberately does
// not use that rule.
func (e *Env) reapBranch(main, branch string, cleared bool) bool {
	if !cleared && !e.Landed(main, branch).Landed {
		return false
	}
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

// ── occupancy ────────────────────────────────────────────────────────────────

// occupancy returns every cwd a live process is sitting in, and whether the
// question could be answered at all.
//
// A zellij pane always has at least its login shell cwd'd into the worktree
// (and the agent as a child), so "some process's cwd is inside this tree" is the
// signal. One dump for the whole sweep, prefix-matched per worktree.
//
// This is the most portability-bound thing holt does — SPEC.md §9 replaces it
// with a heartbeat, with lsof demoted to one provider among several. Until then:
// an empty or failed dump means UNKNOWN, never "nothing is occupied".
func occupancy() (cwds []string, known bool) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, false
	}
	out, err := exec.Command("lsof", "-w", "-d", "cwd", "-F", "n").Output()
	if err != nil && len(out) == 0 {
		return nil, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			cwds = append(cwds, strings.TrimSpace(line[1:]))
		}
	}
	if len(cwds) == 0 {
		return nil, false
	}
	return cwds, true
}

func isOccupied(cwds []string, path string) bool {
	for _, c := range cwds {
		if c == path || strings.HasPrefix(c, path+"/") {
			return true
		}
	}
	return false
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
