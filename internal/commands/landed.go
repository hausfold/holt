package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nebelhaus/holt/internal/gitx"
)

// This file answers the one question the whole safety story rests on: has this
// branch's work already landed on the default branch? It decides whether a
// branch DIES, so a wrong answer in the permissive direction destroys work.
//
// The full merge-strategy matrix is SPEC.md §3. In short:
//
//	fast-forward / merge commit / rebase-that-kept-commits → ancestry, offline
//	forge rebase / squash                                  → the merged PR's headRefOid
//	cherry-pick / rebase done elsewhere                    → patch-equivalence
//	local squash with no PR                                → merge-tree-empty, advisory only
//	forge unreachable                                      → NOT landed. Keep.
//
// Uncertainty always resolves to "not landed", in every branch of this file.

// Verdict is how confident holt is that a branch has landed.
type Verdict struct {
	Landed     bool
	Via        string // ancestry | pr-head-oid | patch-equivalence | merge-tree-empty
	Confidence string // certain | heuristic
	PR         int
}

// forgeTimeout bounds every forge call. A stalled network must never hang a
// pane's teardown — the remove hook runs while zellij waits.
const forgeTimeout = 6 * time.Second

// cacheTTL is how long a forge answer is reused. `reap` sets it to 0: a sweep
// that deletes branches must ask fresh.
var cacheTTL = 120 * time.Second

// Landed reports whether branch has already landed in main's default branch.
func (e *Env) Landed(main, branch string) Verdict {
	base := gitx.DefaultBranch(main)

	// 1. Ancestry — offline, exact, always safe.
	if gitx.IsAncestor(main, branch, base) {
		return Verdict{Landed: true, Via: "ancestry", Confidence: "certain"}
	}

	// 2. The branch's merged PR. Authoritative for squash and forge-rebase
	//    merges, and it survives the remote branch being deleted on merge.
	//    Landed ONLY when the local tip is exactly what that PR merged: a tip
	//    that moved on (post-merge commits, an auto-wip commit) means there is
	//    un-landed work here.
	if state, head, pr := e.mergedPR(main, branch); state == "MERGED" && head != "" {
		if tip := gitx.Rev(main, branch); tip != "" && tip == head {
			return Verdict{Landed: true, Via: "pr-head-oid", Confidence: "certain", PR: pr}
		}
	}

	// 3. Patch-equivalence. `git cherry` marks every commit whose patch-id
	//    already exists upstream with '-'; all of them '-' means the work is
	//    upstream under different SHAs — a cherry-pick, or a rebase somebody
	//    did elsewhere. Offline, and no forge needed.
	if patchEquivalent(main, base, branch) {
		return Verdict{Landed: true, Via: "patch-equivalence", Confidence: "certain"}
	}

	// 4. merge-tree-empty. Strategy-agnostic and offline, but it cannot tell a
	//    squash merge from a branch that never did anything, so it is reported
	//    and never acted on — `reap` ignores it without --contained.
	if mergeTreeEmpty(main, base, branch) {
		return Verdict{Landed: false, Via: "merge-tree-empty", Confidence: "heuristic"}
	}

	return Verdict{Landed: false}
}

// patchEquivalent reports whether every commit on branch has a patch-id
// equivalent already in base. An empty branch is not "landed" — it is empty —
// so a branch with no commits of its own returns false.
func patchEquivalent(main, base, branch string) bool {
	out, err := gitx.Run(main, "cherry", base, branch)
	if err != nil {
		return false
	}
	lines := gitx.Lines(out)
	if len(lines) == 0 {
		return false
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "-") {
			return false
		}
	}
	return true
}

// mergeTreeEmpty reports whether merging branch into base would add nothing.
//
// True for a squash merge, a manual re-implementation, and an empty branch
// alike — which is exactly why the caller treats it as advisory.
func mergeTreeEmpty(main, base, branch string) bool {
	merged, err := gitx.Run(main, "merge-tree", "--write-tree", base, branch)
	if err != nil {
		return false // a conflict exits non-zero, and a conflict is not "landed"
	}
	// --write-tree prints the resulting tree OID on the first line.
	lines := gitx.Lines(merged)
	if len(lines) == 0 {
		return false
	}
	baseTree, err := gitx.Run(main, "rev-parse", base+"^{tree}")
	if err != nil {
		return false
	}
	return lines[0] == baseTree
}

var mergeInfoRe = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\d+)`)

// mergedPR asks the forge for this branch's merged PR: its state, the SHA it
// merged, and its number.
//
// The argv is GitHub-shaped and hardcoded for 0.1; it becomes one forge adapter
// TOML in 0.2 (SPEC.md §5.4), which is why the shape is kept in one place.
func (e *Env) mergedPR(main, branch string) (state, headOID string, pr int) {
	slug, err := gitx.RemoteSlug(main)
	if err != nil || slug == "" {
		return "", "", 0
	}
	out := e.cachedForge("head-"+slug+"-"+branch,
		"pr", "list", "-R", slug, "--head", branch, "--state", "merged", "--limit", "1",
		"--json", "number,state,headRefOid",
		"--jq", `.[0] // empty | "\(.state) \(.headRefOid) \(.number)"`)
	m := mergeInfoRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return "", "", 0
	}
	n, _ := strconv.Atoi(m[3])
	return m[1], m[2], n
}

// cachedForge runs a forge query, memoised on disk.
//
// On disk rather than in memory because the cache must span invocations: the
// statusline refresher and a `holt` listing seconds apart should cost one query
// between them, not two. A failed query writes an empty file only when nothing
// is cached, so an offline run asks once rather than once per row — and never
// clobbers a good answer with an empty one.
func (e *Env) cachedForge(key string, args ...string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, key)
	// Dot-prefixed so the $BASE/*/* worktree globs never see it.
	file := filepath.Join(e.Base, ".cache", safe)

	if cacheTTL > 0 {
		if fi, err := os.Stat(file); err == nil && time.Since(fi.ModTime()) < cacheTTL {
			if b, err := os.ReadFile(file); err == nil {
				return string(b)
			}
		}
	}
	if _, err := exec.LookPath("gh"); err != nil {
		e.Warn("no forge CLI on PATH — PR state is unknown, so nothing will be reaped on that basis")
		return ""
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o755)

	cmd := exec.Command("gh", args...)
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() { out, runErr = cmd.Output(); close(done) }()
	select {
	case <-done:
	case <-time.After(forgeTimeout):
		_ = cmd.Process.Kill()
		<-done
		e.Warn("the forge timed out — PR state is stale")
		runErr = os.ErrDeadlineExceeded
	}
	if runErr != nil {
		if _, err := os.Stat(file); err != nil {
			_ = os.WriteFile(file, nil, 0o644)
		}
		b, _ := os.ReadFile(file)
		return string(b)
	}
	_ = os.WriteFile(file, out, 0o644)
	return string(out)
}
