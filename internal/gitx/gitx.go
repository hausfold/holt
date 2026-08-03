// Package gitx is holt's git layer: subprocess calls to the real `git`, never
// a reimplementation of it.
//
// Shelling out is a deliberate choice, not laziness. holt's whole job is to
// agree with what the user's git does — their config, their hooks, their
// credential helper, their version's merge semantics. A pure-Go git library
// agrees with a *model* of git, and every place the model drifts is a place
// holt reaps a branch git would have called unmerged.
package gitx

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes git in dir and returns trimmed stdout. stderr is folded into the
// error so callers can report why something failed.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// OK reports whether the command succeeded, discarding its output. For the
// many git calls that are questions rather than requests.
func OK(dir string, args ...string) bool {
	_, err := Run(dir, args...)
	return err == nil
}

// Lines splits trimmed output into non-empty lines.
func Lines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// MainCheckout returns the main checkout backing any worktree of a repo.
//
// It must FAIL — not fall back — when dir is gone or isn't a repo. Without
// that, the caller resolves an empty path against its own cwd, and a stale
// registry row pointing at a deleted checkout makes holt list the current
// repo's branches a second time under a repo literally named ".". That was a
// real bug in the bash version (nebelhaus#131) and the fix belongs here, once.
func MainCheckout(dir string) (string, error) {
	common, err := Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if common == "" {
		return "", errors.New("git gave no common dir for " + dir)
	}
	return filepath.Dir(common), nil
}

// Toplevel is the working-tree root of the repo containing dir.
func Toplevel(dir string) (string, error) {
	return Run(dir, "rev-parse", "--show-toplevel")
}

// CurrentBranch is the checked-out branch, or "" on a detached HEAD.
func CurrentBranch(dir string) string {
	b, err := Run(dir, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return b
}

// DefaultBranch is the branch a PR in this repo would land on.
//
// NOT `symbolic-ref HEAD`: that is whatever the main checkout happens to have
// checked out right now. Land a side branch there that happens to contain an
// agent branch, and the agent branch reads as merged though it never reached
// main — and gets reaped, taking the only copy of that work with it.
func DefaultBranch(main string) string {
	if d, err := Run(main, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && d != "" {
		return strings.TrimPrefix(d, "origin/")
	}
	for _, d := range []string{"main", "master", "trunk"} {
		if OK(main, "show-ref", "-q", "--verify", "refs/heads/"+d) {
			return d
		}
	}
	// No conventional default and no origin/HEAD (a fresh repo, an odd remote):
	// HEAD is the best guess left.
	if d, err := Run(main, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		return d
	}
	return "HEAD"
}

// IsAncestor reports whether ref is reachable from base — the offline,
// always-safe half of the landed question (SPEC.md §3): fast-forward, merge
// commit, and any rebase that kept the commits.
func IsAncestor(dir, ref, base string) bool {
	return OK(dir, "merge-base", "--is-ancestor", ref, base)
}

// HasBranch reports whether a local branch exists.
func HasBranch(dir, branch string) bool {
	return OK(dir, "show-ref", "-q", "--verify", "refs/heads/"+branch)
}

// Rev resolves a revision to a full OID, or "" if it doesn't resolve.
func Rev(dir, rev string) string {
	oid, err := Run(dir, "rev-parse", rev)
	if err != nil {
		return ""
	}
	return oid
}

// ShortRev resolves a revision to an abbreviated OID.
func ShortRev(dir, rev string) string {
	oid, err := Run(dir, "rev-parse", "--short", rev)
	if err != nil {
		return ""
	}
	return oid
}

// Porcelain returns `git status --porcelain` output; "" means a clean tree.
func Porcelain(dir string) string {
	s, err := Run(dir, "status", "--porcelain")
	if err != nil {
		return ""
	}
	return s
}

// Dirty reports whether the working tree has any change at all, tracked or not.
func Dirty(dir string) bool { return Porcelain(dir) != "" }

// HasCommits reports whether HEAD resolves — false in a repo with no commits
// yet, where a worktree has to be created with --orphan.
func HasCommits(dir string) bool {
	return OK(dir, "rev-parse", "--verify", "HEAD")
}

// Subject is the first line of a revision's commit message.
func Subject(dir, rev string) string {
	s, err := Run(dir, "log", "-1", "--format=%s", rev)
	if err != nil {
		return ""
	}
	return s
}

// PushedAnywhere reports whether a commit is contained in any remote-tracking
// branch. Rewriting such a commit turns "give me my files back" into a
// force-push, so unpark refuses it.
func PushedAnywhere(dir, rev string) bool {
	out, err := Run(dir, "branch", "-r", "--contains", rev)
	return err == nil && out != ""
}

// RemoteSlug is owner/name parsed from a remote URL, for the forge adapter.
//
// This — not the directory basename — is a repo's identity (SPEC.md §4). Two
// checkouts named `api` under different orgs is the common case, not the
// exotic one.
func RemoteSlug(dir string) (string, error) {
	var url string
	var err error
	for _, remote := range []string{"origin", "upstream"} {
		if url, err = Run(dir, "remote", "get-url", remote); err == nil && url != "" {
			break
		}
	}
	if url == "" {
		// Any remote at all, alphabetically, before giving up.
		names, nerr := Run(dir, "remote")
		if nerr != nil || names == "" {
			return "", errors.New("no remote to take an identity from")
		}
		if url, err = Run(dir, "remote", "get-url", Lines(names)[0]); err != nil {
			return "", err
		}
	}
	return ParseSlug(url), nil
}

// ParseSlug strips scheme, user and host off a remote URL, leaving owner/name.
func ParseSlug(url string) string {
	url = strings.TrimSuffix(url, ".git")
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	}
	if i := strings.LastIndex(url, "@"); i >= 0 {
		url = url[i+1:]
	}
	// Drop host + its separator (':' for scp-style, '/' for URLs).
	if i := strings.IndexAny(url, ":/"); i >= 0 {
		url = url[i+1:]
	}
	return strings.Trim(url, "/")
}
