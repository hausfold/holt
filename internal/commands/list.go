package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/ui"
)

// List renders every live/parked agent worktree across every repo holt can
// reach, and self-heals on the way in.
func (e *Env) List(asJSON bool) error {
	// Self-heal: reap parked branches whose PR has since merged. PARKED ONLY —
	// it must never disturb a live checkout that may still have an open pane.
	// The riskier live sweep is opt-in via `holt reap`. Best-effort: a network
	// hiccup must not break the listing.
	sweep := e.reapSweep(sweepParked)
	if !asJSON {
		if n := len(sweep.Reaped); n > 0 {
			ui.Say("swept %d merged worktree(s)", n)
		}
		// Husks are deliberately left by the sweep, so the listing is where they
		// surface — otherwise a half-removed checkout is a `stray` row with no
		// hint of what to do about it.
		if n := len(sweep.Strays); n > 0 {
			ui.Say("%d dangling checkout(s) — git lost the link; `holt <name>` moves each aside and rebuilds:", n)
			for _, s := range sweep.Strays {
				ui.Say("  %s", s)
			}
		}
	}

	rows := e.rows()
	if asJSON {
		return e.listJSON(rows)
	}

	ui.Say("agent worktrees you can resume (holt <name>, or <repo>/<name>)")
	if len(rows) == 0 {
		ui.Say("none parked — every worktree branch is merged & cleaned up. The fog is even.")
		return nil
	}
	renderTable(rows)
	return nil
}

// listRow is one rendered line's worth of facts.
type listRow struct {
	Repo     string
	Name     string
	State    string
	Agent    string
	Last     string
	Entry    Entry
	Ahead    int
	AheadPR  int
	Relanded bool
}

func (e *Env) rows() []listRow {
	var out []listRow
	for _, entry := range e.discover() {
		if !e.branchAlive(entry) {
			continue
		}
		// The state cell is the CHECKOUT's state — plus, when it applies, the
		// one fact invisible everywhere else: this branch's PR already merged
		// and it has committed since. Without the marker such a row is
		// indistinguishable from an ordinary in-flight branch, which is exactly
		// how un-shipped commits go unnoticed until someone tidies up by hand.
		state := string(entry.State)
		n, pr := e.postMergeAhead(entry.Main, entry.Branch)
		row := listRow{
			Repo:  filepath.Base(entry.Main),
			Name:  entry.Name(),
			State: state,
			Entry: entry,
		}
		if n > 0 {
			row.State = state + "+" + strconv.Itoa(n)
			row.Ahead, row.AheadPR, row.Relanded = n, pr, true
		}
		row.Agent = e.agentFor(entry.Path)
		// Empty for an unborn branch (a session started in a repo with no
		// commits yet) — say so rather than printing a blank cell that reads
		// like a broken row.
		last, err := gitx.Run(entry.Main, "log", "-1", "--format=%cr — %s", entry.Branch)
		if err != nil || last == "" {
			last = "no commits yet"
		}
		row.Last = last
		out = append(out, row)
	}
	return out
}

// agentFor is the client recorded for a worktree. A registry row that predates
// the client column means Claude, never today's default — otherwise a parked
// Codex branch would reopen in the wrong client.
func (e *Env) agentFor(path string) string {
	if row, ok := e.Reg.Find(path); ok {
		return row.Agent
	}
	return e.Agent
}

// branchAlive reports whether a row still means something.
//
// Normally "does refs/heads/<branch> exist" — a branch that was merged and
// deleted, or hand-nuked, can't resume anything. EXCEPT a worktree spawned in a
// repo with no commits yet: its branch is UNBORN, checked out with no ref behind
// it until the first commit lands. By ref alone such a session reads as dead the
// moment it starts.
func (e *Env) branchAlive(entry Entry) bool {
	if gitx.HasBranch(entry.Main, entry.Branch) {
		return true
	}
	if entry.State != Live {
		return false
	}
	head, err := gitx.Run(entry.Path, "symbolic-ref", "--short", "HEAD")
	return err == nil && head == entry.Branch
}

// postMergeAhead names the case reaping refuses to touch: the PR merged, then
// the session kept committing. Those commits sit on a branch whose remote
// counterpart the forge deleted at merge — no PR covers them, nothing is pushed,
// and the only symptom used to be a worktree that quietly declined to be reaped.
// Returns (0, 0) when this isn't that case.
func (e *Env) postMergeAhead(main, branch string) (ahead, pr int) {
	// Landed by ancestry beats everything: if the tip is already IN the default
	// branch, those later commits landed too (a second PR, a direct merge) and
	// there is nothing to ship. Local, cheap, and asked FIRST so the marker can
	// never contradict the sweep that would reap this branch.
	if gitx.IsAncestor(main, branch, gitx.DefaultBranch(main)) {
		return 0, 0
	}
	head, num := e.mergedMapLookup(main, branch)
	if head == "" {
		return 0, 0
	}
	tip := gitx.Rev(main, branch)
	if tip == "" || tip == head {
		return 0, 0
	}
	// The merged SHA is normally an ancestor of the tip (we committed on top of
	// it), so this count is exact. When it isn't reachable at all — the branch
	// was rebased or amended after the merge — say 1, because "at least one
	// commit here is not what landed" is the part that is certainly true.
	n := 1
	if out, err := gitx.Run(main, "rev-list", "--count", head+".."+branch); err == nil {
		if parsed, err := strconv.Atoi(out); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return n, num
}

// mergedMapLookup asks ONE repo-wide question rather than one per branch.
//
// A per-branch query costs ~0.5 s, and the listing asks this of every row — a
// repo with eight worktrees would go from 0.3 s to seconds, in exactly the fog
// where you most want a fast listing. `Landed` deliberately keeps its own
// precise per-branch query: it decides whether a branch DIES, so it must not
// inherit this one's 100-PR horizon. Missing a merge here costs an annotation;
// missing it there would cost the work.
func (e *Env) mergedMapLookup(main, branch string) (headOID string, pr int) {
	slug, err := gitx.RemoteSlug(main)
	if err != nil || slug == "" {
		return "", 0
	}
	out := e.cachedForge("merged-"+slug,
		"pr", "list", "-R", slug, "--state", "merged", "--limit", "100",
		"--json", "number,headRefName,headRefOid",
		"--jq", `.[] | "\(.headRefName)\t\(.headRefOid)\t\(.number)"`)
	for _, line := range gitx.Lines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		// A cross-repo (fork) PR arrives as owner:branch, so compare on the
		// suffix. Without this a fork-merged branch reads as unlanded forever —
		// safe, but the +N marker and the sweep both go blind.
		name := f[0]
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[i+1:]
		}
		if name == branch {
			n, _ := strconv.Atoi(f[2])
			return f[1], n
		}
	}
	return "", 0
}

// ── rendering ────────────────────────────────────────────────────────────────

// renderTable sizes every column to its real content and to the pane, so the
// listing stays one line per worktree however narrow the terminal is.
func renderTable(rows []listRow) {
	rw, nw, sw, cw := 4, 4, 6, 5
	relanded := false
	for _, r := range rows {
		rw = max(rw, len(r.Repo))
		nw = max(nw, len(r.Name))
		sw = max(sw, len(r.State)) // the +N marker makes this content-sized
		cw = max(cw, len(r.Agent))
		relanded = relanded || r.Relanded
	}
	rw = min(rw, 16)

	cols := terminalWidth()

	// Cap `name` — the widest-varying column — as a function of the PANE, not a
	// constant. A flat cap clipped a 29-char name in a 130-column pane with 40
	// columns still unspent, and the truncated name is exactly the argument you
	// then have to type at `holt <name>`.
	nwCap := cols - (2 + rw + 1 + 1 + sw + 1 + cw + 1) - 24
	nwCap = max(nwCap, 28)
	nw = min(nw, nwCap)

	// Drop the client column first when space is tight, then let the commit take
	// whatever is left. 2 = indent, +1 per inter-column gap.
	showAgent := true
	used := 2 + rw + 1 + nw + 1 + sw + 1 + cw + 1
	if cols-used < 20 {
		showAgent = false
		used = 2 + rw + 1 + nw + 1 + sw + 1
	}
	lastw := cols - used
	if lastw < 12 {
		// Truly tight: the fixed columns alone leave no room for the commit.
		// `name` is the next most compressible, so shrink it to buy the commit a
		// legible slice rather than overflow the line.
		fixed := 2 + rw + 1 + 1 + sw + 1
		if showAgent {
			fixed += cw + 1
		}
		nw = max(cols-fixed-12, 8)
		lastw = 12
	}

	if showAgent {
		f := fmt.Sprintf("  %%-%ds %%-%ds %%-%ds %%-%ds %%s\n", rw, nw, sw, cw)
		ui.Out(f, "repo", "name", "state", "agent", "last commit")
		for _, r := range rows {
			ui.Out(f, fit(r.Repo, rw), fit(r.Name, nw), r.State, r.Agent, fit(r.Last, lastw))
		}
	} else {
		f := fmt.Sprintf("  %%-%ds %%-%ds %%-%ds %%s\n", rw, nw, sw)
		ui.Out(f, "repo", "name", "state", "last commit")
		for _, r := range rows {
			ui.Out(f, fit(r.Repo, rw), fit(r.Name, nw), r.State, fit(r.Last, lastw))
		}
	}
	// Only ever printed when a row earned it, so the listing stays a table on a
	// normal day — and the day it isn't normal, the fix is one command away.
	if relanded {
		ui.Say("+N = commits landed AFTER that branch's PR merged — no PR covers them: holt reship <name>")
	}
}

// fit trims a string to width, marking it when it overflowed.
func fit(s string, n int) string {
	if n < 1 {
		n = 1
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func terminalWidth() int {
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	if out, err := exec.Command("tput", "cols").Output(); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && n > 0 {
			return n
		}
	}
	return 80
}
