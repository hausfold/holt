package commands

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/registry"
	"github.com/nebelhaus/holt/internal/ui"
)

// New, Spawn and Child are three answers to "who is asking", and they differ in
// exactly two ways: what goes in the registry's PARENT field, and whether a
// taken name is fatal.
//
//	New    — a pane pressed the key. Parent is the PANE's cwd, so the statusline
//	         files the worktree under the session that opened it. Then it EXECS
//	         the client, becoming that session.
//	Child  — a pane working on ANOTHER repo. Same parent rule, so the child's PR
//	         surfaces under the session that spawned it. Prints the path so the
//	         caller can `cd "$(holt child …)"`. A taken name is fatal: there is
//	         a human here to tell.
//	Spawn  — nobody's pane (the palette's command runs under launchd). The
//	         session it launches is TOP-LEVEL, so the parent is the repo's own
//	         main checkout — a pane sitting in that repo lists it, which is where
//	         a human looks. A taken name takes the next free suffix, because a
//	         palette has nobody to tell and a dead end there is a command that
//	         silently did nothing.

// New makes a worktree of THIS repo and opens the default client in it.
//
// This is the client-agnostic spawn bind. Claude Code has a native --worktree
// flag that fires the create hook; Codex and OpenCode have nothing like it, so a
// machine whose default is one of those had a headline keybind that launched a
// client it may not even have installed.
func (e *Env) New(want, agentID string) error {
	agentID = orDefault(agentID, e.Agent)
	if _, ok := specFor(agentID); !ok {
		return exitcode.Usagef("unknown agent %q (expected claude, codex, or opencode)", agentID)
	}
	main, err := e.mainCheckoutOf(e.Cwd, true)
	if err != nil {
		return err
	}

	name, dir, err := e.freeName(main, orDefault(want, randomName()))
	if err != nil {
		return err
	}
	if err := e.addWorktree(main, name, dir); err != nil {
		return err
	}
	_ = e.Reg.Put(registry.Row{
		Name: name, Main: main, Branch: "worktree-" + name,
		Path: dir, Parent: e.Cwd, Agent: agentID,
	})
	ui.Say("created %s worktree '%s' → %s", filepath.Base(main), name, dir)

	// The client is resolved LAST, and its absence is not fatal to the worktree:
	// the checkout and the registry row are already on disk, so an uninstalled
	// client costs you this launch, not the branch. `holt <name>` picks it up.
	spec, err := resolveAgent(agentID)
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return exitcode.Usagef("could not enter %s", dir)
	}
	return execClient(spec.open)
}

// Child makes a worktree of ANOTHER repo, as a child of this pane.
//
// The cross-repo escape hatch. A workshop pane whose task belongs to a sub-repo
// would otherwise reach for a raw `git worktree add` — which never touches the
// registry, so nothing ever learns to ask THAT repo's forge for the branch's PR,
// and the statusline stays blind to it.
func (e *Env) Child(target, want string) error {
	if target == "" {
		return exitcode.Usagef("usage: holt child <repo-path> [name]")
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return exitcode.Usagef("no such directory: %s", target)
	}
	main, err := e.mainCheckoutOf(target, false)
	if err != nil {
		return err
	}

	// Default the child's name to THIS pane's own worktree name, so a sub-worktree
	// shares the session's identity (…-sparkle in both repos).
	if want == "" {
		if b := gitx.CurrentBranch(e.Cwd); len(b) > 9 && b[:9] == "worktree-" {
			want = b[9:]
		} else {
			want = filepath.Base(e.Cwd)
		}
	}

	dir := filepath.Join(e.Base, e.bucketFor(main), want)
	if _, err := os.Stat(dir); err == nil {
		return exitcode.Usagef("a worktree already exists at %s — pass another name: holt child %s <name>", dir, target)
	}
	if gitx.HasBranch(main, "worktree-"+want) {
		return exitcode.Usagef("branch worktree-%s already exists in %s — pass another name: holt child %s <name>",
			want, filepath.Base(main), target)
	}
	if err := e.addWorktree(main, want, dir); err != nil {
		return err
	}
	// Registered with THIS pane's cwd as parent — the same field the create hook
	// stores — so the statusline lists the child under the session that spawned
	// it, and queries the CHILD repo's forge for its PR state.
	_ = e.Reg.Put(registry.Row{
		Name: want, Main: main, Branch: "worktree-" + want,
		Path: dir, Parent: e.Cwd, Agent: e.agentForPath(e.Cwd),
	})
	ui.Say("created %s worktree '%s' → %s", filepath.Base(main), want, dir)
	ui.Out("%s\n", dir) // ONLY the path on stdout, so: cd "$(holt child …)"
	return nil
}

// Spawn makes a NAMED worktree for a spawner that has no pane of its own.
func (e *Env) Spawn(target, want, agentID string) error {
	if target == "" || want == "" {
		return exitcode.Usagef("usage: holt spawn <repo-path> <name>")
	}
	agentID = orDefault(agentID, e.Agent)
	if _, ok := specFor(agentID); !ok {
		return exitcode.Usagef("unknown agent %q (expected claude, codex, or opencode)", agentID)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return exitcode.Usagef("no such directory: %s", target)
	}
	main, err := e.mainCheckoutOf(target, false)
	if err != nil {
		return err
	}
	name, dir, err := e.freeName(main, want)
	if err != nil {
		return err
	}
	if err := e.addWorktree(main, name, dir); err != nil {
		return err
	}
	_ = e.Reg.Put(registry.Row{
		Name: name, Main: main, Branch: "worktree-" + name,
		Path: dir, Parent: main, Agent: agentID,
	})
	ui.Say("created %s worktree '%s' → %s", filepath.Base(main), name, dir)
	ui.Out("%s\n", dir)
	return nil
}

// ── shared plumbing ──────────────────────────────────────────────────────────

// mainCheckoutOf resolves a path to the main checkout of its repo, refusing
// anything that isn't one.
//
// `here` distinguishes the two callers, and the wording is the whole point: for
// `new` the offending path is the pane's own cwd and the fix is to cd somewhere
// else, while for `child`/`spawn` it is an argument the caller passed and has to
// be named back to them.
func (e *Env) mainCheckoutOf(path string, here bool) (string, error) {
	main, err := gitx.MainCheckout(path)
	if err != nil {
		if here {
			return "", exitcode.Usagef("not inside a git repo — cd to one first")
		}
		return "", exitcode.Usagef("'%s' isn't inside a git repo", path)
	}
	if fi, err := os.Stat(filepath.Join(main, ".git")); err != nil || !fi.IsDir() {
		return "", exitcode.Usagef("'%s' resolves to %s, which isn't a main checkout", path, main)
	}
	return main, nil
}

// bucketFor is the directory a repo's worktrees live under.
//
// The repo's basename, EXCEPT when that would collide with the spawning pane's
// own repo basename (the nested case: a workshop named `nebelhaus` holding a
// rice also named `nebelhaus`) — then the full owner-repo slug, so the child
// never lands on the parent's own checkout path.
//
// Buckets are COSMETIC: every command re-derives a worktree's main checkout from
// the checkout itself, never from the path. SPEC.md §4 makes the slug
// unconditional; until then this keeps existing checkouts where they are.
func (e *Env) bucketFor(main string) string {
	bucket := filepath.Base(main)
	if cur, err := gitx.MainCheckout(e.Cwd); err == nil && filepath.Base(cur) == bucket && cur != main {
		if slug, err := gitx.RemoteSlug(main); err == nil && slug != "" {
			return filepath.Join(sanitizeSlug(slug))
		}
	}
	return bucket
}

func sanitizeSlug(slug string) string {
	out := []rune(slug)
	for i, r := range out {
		if r == '/' {
			out[i] = '-'
		}
	}
	return string(out)
}

// freeName finds the first name near `want` with neither a checkout nor a branch
// already using it, and returns it with its checkout path.
func (e *Env) freeName(main, want string) (name, dir string, err error) {
	bucket := e.bucketFor(main)
	name = want
	for n := 1; ; n++ {
		dir = filepath.Join(e.Base, bucket, name)
		_, statErr := os.Stat(dir)
		if statErr != nil && !gitx.HasBranch(main, "worktree-"+name) {
			return name, dir, nil
		}
		if n > 99 {
			return "", "", exitcode.Usagef("no free name near '%s' in %s", want, bucket)
		}
		name = want + "-" + strconv.Itoa(n+1)
	}
}

// randomName gives an unnamed spawn a throwaway two-word name, in the spirit of
// the ones Claude generates. It only has to be recognisable in a listing and on
// a branch for as long as the work lives — `holt spawn` (the palette) is where
// names come from the TASK; this is the "just give me a pane" path, so it
// doesn't ask for one.
func randomName() string {
	adjectives := []string{
		"amber", "brisk", "calm", "dusky", "eager", "fleet", "glassy", "hushed",
		"inky", "jade", "keen", "lucid", "misty", "noble", "opal", "quiet",
		"rusty", "slate", "tidal", "umber", "vivid",
	}
	nouns := []string{
		"alder", "beacon", "cinder", "delta", "ember", "fjord", "grove",
		"harbor", "inlet", "juniper", "kelp", "lantern", "meadow", "nimbus",
		"orchid", "pebble", "quarry", "ridge", "summit", "thicket",
	}
	return adjectives[rand.IntN(len(adjectives))] + "-" + nouns[rand.IntN(len(nouns))]
}
