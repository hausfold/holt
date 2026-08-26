package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hausfold/holt/internal/gitx"
	"github.com/hausfold/holt/internal/ui"
)

// List renders every live/parked lane across every repo holt can reach, and
// self-heals on the way in.
func (e *Env) List(asJSON bool) error {
	// Self-heal: reap parked branches whose PR has since merged. PARKED ONLY —
	// it must never disturb a live checkout that may still have an open pane.
	// The riskier live sweep is opt-in via `holt reap`. Best-effort: a network
	// hiccup must not break the listing.
	sweep := e.reapSweep(sweepParked)
	if !asJSON {
		if n := len(sweep.Reaped); n > 0 {
			ui.Say("swept %d merged lane(s)", n)
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

	ui.Say("lanes you can resume (holt <name>, or <repo>/<name>)")
	if len(rows) == 0 {
		ui.Say("none parked — every lane's branch is merged & cleaned up. The fog is even.")
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
	Diverged bool
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
		n, pr, diverged := e.postMergeAhead(entry.Main, entry.Branch)
		row := listRow{
			Repo:  filepath.Base(entry.Main),
			Name:  entry.Name(),
			State: state,
			Entry: entry,
		}
		switch {
		case n > 0 && diverged:
			// Distinct glyph on purpose: a "+N" reader reasonably assumes
			// `holt reship` is the fix, and here it is the wrong one.
			row.State = state + "~" + strconv.Itoa(n)
			row.Ahead, row.AheadPR, row.Diverged = n, pr, true
		case n > 0:
			row.State = state + "+" + strconv.Itoa(n)
			row.Ahead, row.AheadPR, row.Relanded = n, pr, true
		}
		row.Agent = e.agentFor(entry.Path)
		// Empty for an unborn branch (a lane opened in a repo with no
		// commits yet) — say so rather than printing a blank cell that reads
		// like a broken row.
		//
		// The subject alone, NOT git's relative date (`%cr`, "3 minutes
		// ago"): this value is also `--json`'s `last_commit` (json.go), a
		// frozen contract two spellings of the same listing must answer
		// byte-identically (SPEC.md §2.2) and `watch` diffs verbatim to
		// decide whether a lane actually changed (watch.go's `changed`
		// kind). A relative date drifts every second on its own, which
		// broke both: two `holt --json` calls a heartbeat apart could
		// legitimately disagree, and a `watch` consumer would see a
		// `changed` event on every rescan tick even when nothing about the
		// lane had moved.
		last, err := gitx.Run(entry.Main, "log", "-1", "--format=%s", entry.Branch)
		if err != nil || last == "" {
			last = "no commits yet"
		}
		row.Last = last
		out = append(out, row)
	}
	return out
}

// agentFor is the client recorded for a lane. A registry row that predates
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
// deleted, or hand-nuked, can't resume anything. EXCEPT a lane opened in a repo
// with no commits yet: its branch is UNBORN, checked out with no ref behind it
// until the first commit lands. By ref alone such a lane reads as dead the
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
// the lane kept committing. Those commits sit on a branch whose remote
// counterpart the forge deleted at merge — no PR covers them, nothing is pushed,
// and the only symptom used to be a lane that quietly declined to be reaped.
// Returns (0, 0, false) when this isn't that case.
//
// `diverged` is the other way a tip can differ from the merged SHA: not built
// on top of it at all — a second local checkout of the same branch that never
// pulled, a rebase, an amend. `ahead` still counts commits reachable from
// branch but not from head for these, because that count is not FALSE, but it
// answers a different question than "what should I reship" — those commits
// are not new work sitting on the merge, they are a stale or sideways tip.
// Reshipping one would push and PR content the merge already superseded.
func (e *Env) postMergeAhead(main, branch string) (ahead, pr int, diverged bool) {
	// Landed by ancestry beats everything: if the tip is already IN the default
	// branch, those later commits landed too (a second PR, a direct merge) and
	// there is nothing to ship. Local, cheap, and asked FIRST so the marker can
	// never contradict the sweep that would reap this branch.
	base := gitx.DefaultBranch(main)
	if gitx.IsAncestor(main, branch, base) {
		return 0, 0, false
	}
	head, num := e.mergedMapLookup(main, branch)
	if head == "" {
		return 0, 0, false
	}
	tip := gitx.Rev(main, branch)
	if tip == "" || tip == head {
		return 0, 0, false
	}
	// An OPEN PR standing at this exact tip already covers everything since the
	// merge, so the lane is simply in flight — neither behind a reship nor
	// sideways. Without this the marker never comes down: the map it reads
	// lists MERGED PRs only, so the follow-up PR `holt reship` just opened is
	// invisible to it and the lane goes on being told to reship, forever.
	// Compared by OID rather than by mere existence, because a commit made
	// after that push is genuinely uncovered until it is pushed too — and that
	// is exactly the case the marker is for.
	if oid, _ := e.openMapLookup(main, branch); oid != "" && oid == tip {
		return 0, 0, false
	}
	// `--not base` is what keeps the count honest. A long-lived lane pulls the
	// default branch back in — a merge from main, a rebase onto it — and every
	// commit that ride brings along is reachable from the tip but not from the
	// merged head, so a bare `head..branch` counts other people's landed work as
	// this lane's un-shipped commits. That is how a lane with two real commits
	// came to read `live+131`: a number that size reads as "unreviewable, deal
	// with it later" rather than "one PR, two commits". The marker promises
	// "commits no PR covers", so anything already on the default branch — which
	// some PR demonstrably did cover — must not be in it.
	n := 1
	if out, err := gitx.Run(main, "rev-list", "--count", head+".."+branch, "--not", base); err == nil {
		if parsed, err := strconv.Atoi(out); err == nil {
			if parsed == 0 {
				// Every commit since the merge is on the default branch already:
				// this lane has nothing un-shipped, whatever the raw count says.
				return 0, 0, false
			}
			n = parsed
		}
	}
	// The merged SHA is normally an ancestor of the tip (we committed on top of
	// it) — that is the "outran its PR" case reship exists for. When it is not
	// reachable, ancestry alone can't tell WHY: `git rebase main` to catch up
	// (legitimate — the branch's own commit is still there, just replayed onto
	// a new base under a new SHA) looks identical, by ancestry, to a second
	// checkout that pushed a different tip under the same branch name first.
	// `builtOnMerge` tells them apart the way `Landed`'s own ladder does for
	// the same shape of problem (step 3): a rebase preserves the DIFF even
	// though it changes the SHA, so if `head`'s patch already exists among
	// commits unique to this branch, the branch built on the merge after all.
	if !builtOnMerge(main, base, head, branch) {
		return n, num, true
	}
	return n, num, false
}

// builtOnMerge reports whether branch's history — literally, or as an
// equivalent patch after a rebase — includes what actually merged.
func builtOnMerge(main, base, head, branch string) bool {
	// The default branch being an ancestor of the tip settles it before any
	// SHA is compared: everything on the default branch is in this history,
	// and what merged is on the default branch by definition. Neither check
	// below can see that, because a SQUASH merge puts the branch's content on
	// the default branch under a NEW sha whose patch is the whole PR at once —
	// so `head`, the PR's pre-squash tip, is not an ancestor of a branch that
	// has since rebased onto the default branch, and `git cherry` cannot match
	// it against that branch's own commits either. Without this, a lane that
	// did exactly the right thing — squash-merge its PR, rebase onto the
	// default branch, keep working — read as a stale, sideways checkout, and
	// both `reap` and `reship` told it to delete itself rather than ship the
	// real commits sitting on top.
	if gitx.IsAncestor(main, mergeRef(main, base), branch) {
		return true
	}
	if gitx.IsAncestor(main, head, branch) {
		return true
	}
	// "upstream" in git's terms, confusingly: this asks whether head's patch
	// is already applied ON branch, marking it '-' if so. Uncertainty (cherry
	// errors, or head's patch genuinely isn't there) resolves to "not built on
	// the merge" — the same direction everything else in this file resolves
	// uncertainty, because the cost of guessing wrong here is `reship` pushing
	// content the merge already superseded.
	out, err := gitx.Run(main, "cherry", branch, head)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(out), "-")
}

// mergeRef is the ref the ancestry question above should actually be asked
// against: the remote-tracking default branch when there is one.
//
// A worktree-driven repo's LOCAL default branch is routinely stale — nobody
// checks it out, every lane branches from origin — and answering "did this
// build on the merge?" against a stale ref is the one way the check above can
// say yes when the honest answer is no.
func mergeRef(main, base string) string {
	if remote := "origin/" + base; gitx.Rev(main, remote) != "" {
		return remote
	}
	return base
}

// openMapLookup is mergedMapLookup's other half: the tip and number of this
// branch's OPEN PR, if it has one. Same one-query-per-repo shape, for the same
// reason — the listing asks this of every row, and a per-branch query costs
// ~0.5 s each.
func (e *Env) openMapLookup(main, branch string) (headOID string, pr int) {
	slug, err := gitx.RemoteSlug(main)
	if err != nil || slug == "" {
		return "", 0
	}
	out := e.cachedForge(openMapKey(slug),
		"pr", "list", "-R", slug, "--state", "open", "--limit", "100",
		"--json", "number,headRefName,headRefOid",
		"--jq", `.[] | "\(.headRefName)\t\(.headRefOid)\t\(.number)"`)
	for _, line := range gitx.Lines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		// A cross-repo (fork) PR arrives as owner:branch — same suffix compare
		// the merged map does, and for the same reason.
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

// openMapKey is shared with reship, which forgets this entry the moment it
// opens a PR: the cache holds for 120 s, and a marker that goes on demanding
// the reship that just succeeded is exactly the bug this map exists to fix.
func openMapKey(slug string) string { return "open-" + slug }

// mergedMapLookup asks ONE repo-wide question rather than one per branch.
//
// A per-branch query costs ~0.5 s, and the listing asks this of every row — a
// repo with eight lanes would go from 0.3 s to seconds, in exactly the fog
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
// listing stays one line per lane however narrow the terminal is.
func renderTable(rows []listRow) {
	rw, nw, sw, cw := 4, 4, 6, 5
	relanded, diverged := false, false
	for _, r := range rows {
		rw = max(rw, len(r.Repo))
		nw = max(nw, len(r.Name))
		sw = max(sw, len(r.State)) // the +N / ~N marker makes this content-sized
		cw = max(cw, len(r.Agent))
		relanded = relanded || r.Relanded
		diverged = diverged || r.Diverged
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
	if diverged {
		ui.Say("~N = the tip does not build on that branch's merged PR — a stale or sideways checkout, not new work. Its content already landed; remove the checkout instead of reshipping it.")
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
