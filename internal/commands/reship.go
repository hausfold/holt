package commands

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/ui"
)

// Reship pushes a branch that outran its merged PR, and opens the follow-up.
//
// The other half of the +N story. After a squash merge the forge deletes the
// head branch, so the commits a session makes afterwards have no remote and no
// PR — `git push` alone re-creates the branch but leaves the work unreviewed and
// invisible. This does both, from the MAIN checkout, so it works whether the
// worktree is live, parked, or long gone.
func (e *Env) Reship(want string) error {
	main, branch, err := e.reshipTarget(want)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return exitcode.Degradedf("gh is unavailable — install it, or push and open the PR by hand.")
	}
	slug, err := gitx.RemoteSlug(main)
	if err != nil || slug == "" {
		return exitcode.Usagef("that repo has no origin remote — nothing to push to.")
	}
	base := gitx.DefaultBranch(main)

	// "Nothing to ship" is the answer whenever the branch adds nothing to the
	// base — true both for a fully-landed branch and for one that never
	// diverged. Asked BEFORE the push, so a no-op can't leave a pushed branch
	// with no PR behind it.
	if countCommits(main, base+".."+branch) == 0 {
		return exitcode.Refusedf("'%s' has nothing the %s branch doesn't already have.", branch, base)
	}

	ui.Say("pushing %s → origin (%s)", branch, slug)
	if _, err := gitx.Run(main, "push", "-u", "origin", branch); err != nil {
		return exitcode.Usagef("push failed — resolve it, then re-run: holt reship (%v)", err)
	}

	// An OPEN PR already covers these commits; the push above was the whole job.
	if url := e.openPRFor(slug, branch); url != "" {
		ui.Say("an open PR already covers this branch — pushed to it: %s", url)
		return nil
	}

	ahead, prNum := e.postMergeAhead(main, branch)
	title := gitx.Subject(main, branch)
	if title == "" {
		title = "follow-up on " + branch
	}

	url, err := ghCreatePR(slug, branch, base, title, reshipBody(main, base, branch, prNum))
	if err != nil {
		return exitcode.Usagef("gh pr create failed: %v", err)
	}
	suffix := ""
	if ahead > 0 {
		suffix = " for the " + strconv.Itoa(ahead) + " commit(s) past the merge"
	}
	ui.Say("follow-up PR open%s: %s", suffix, url)
	return nil
}

// reshipTarget resolves which (main, branch) to reship: a named worktree, or —
// with no name — the branch of the checkout we are standing in.
func (e *Env) reshipTarget(want string) (main, branch string, err error) {
	if want == "" {
		main, err = gitx.MainCheckout(e.Cwd)
		if err != nil {
			return "", "", exitcode.Usagef("not in a git repo — name a worktree instead: holt reship <name>")
		}
		branch = gitx.CurrentBranch(e.Cwd)
		if branch == "" {
			return "", "", exitcode.Refusedf("HEAD is detached — check out a branch first.")
		}
		return main, branch, nil
	}

	repo, name := "", want
	if i := strings.Index(want, "/"); i >= 0 {
		repo, name = want[:i], want[i+1:]
	}
	var matches []Entry
	for _, entry := range e.discover() {
		if !e.branchAlive(entry) || entry.Name() != name {
			continue
		}
		if repo != "" && baseName(entry.Main) != repo {
			continue
		}
		matches = append(matches, entry)
	}
	switch len(matches) {
	case 0:
		return "", "", exitcode.Usagef("no agent worktree named '%s' — run: holt", want)
	case 1:
		return matches[0].Main, matches[0].Branch, nil
	default:
		return "", "", exitcode.Usagef("'%s' exists in more than one repo — qualify it: holt reship <repo>/%s", name, name)
	}
}

// reshipBody is a PR body holt can write HONESTLY: what this PR carries, and
// what it follows. The What / Why / Verify / Watch-out a reviewer is owed is
// prompted for, not faked — holt did not write the code and has nothing true to
// say about why it exists.
func reshipBody(main, base, branch string, prNum int) string {
	after := ""
	if prNum > 0 {
		after = "PR #" + strconv.Itoa(prNum) + " "
	}
	log, _ := gitx.Run(main, "log", "--format=- %s", base+".."+branch)
	lines := gitx.Lines(log)
	if len(lines) > 20 {
		lines = lines[:20]
	}
	return "Commits on `" + branch + "` that landed after " + after + "merged:\n\n" +
		strings.Join(lines, "\n") +
		"\n\n_Opened by `holt reship` — add What / Why / Verify / Watch-out._\n"
}

func (e *Env) openPRFor(slug, branch string) string {
	out, err := exec.Command("gh", "pr", "list", "-R", slug, "--head", branch,
		"--state", "open", "--limit", "1", "--json", "url", "--jq", ".[0].url // empty").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ghCreatePR(slug, head, base, title, body string) (string, error) {
	out, err := exec.Command("gh", "pr", "create", "-R", slug,
		"--head", head, "--base", base, "--title", title, "--body", body).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", errorText(text, err)
	}
	// gh prints progress before the URL; the URL is the last line.
	if lines := gitx.Lines(text); len(lines) > 0 {
		return lines[len(lines)-1], nil
	}
	return text, nil
}

func countCommits(dir, rng string) int {
	out, err := gitx.Run(dir, "rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return n
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func errorText(text string, err error) error {
	if text != "" {
		return &plainError{text}
	}
	return err
}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }
