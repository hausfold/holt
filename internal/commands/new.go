package commands

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hausfold/holt/internal/config"
	"github.com/hausfold/holt/internal/exitcode"
	"github.com/hausfold/holt/internal/gitx"
	"github.com/hausfold/holt/internal/registry"
	"github.com/hausfold/holt/internal/ui"
)

// New, Spawn and Child are three answers to "who is asking", and they differ in
// exactly two ways: what goes in the registry's PARENT field, and whether a
// taken name is fatal.
//
//	New    — a pane pressed the key. Parent is the PANE's cwd, so the statusline
//	         files the lane under the pane that opened it. Then it EXECS the
//	         client, becoming that pane's session.
//	Child  — a pane working on ANOTHER repo. Same parent rule, so the child's PR
//	         surfaces under the lane that spawned it. Prints the path so the
//	         caller can `cd "$(holt child …)"`. A taken name is fatal: there is
//	         a human here to tell.
//	Spawn  — nobody's pane (the palette's command runs under launchd). The lane
//	         it opens is TOP-LEVEL, so the parent is the repo's own main
//	         checkout — a pane sitting in that repo lists it, which is where a
//	         human looks. A taken name takes the next free suffix, because a
//	         palette has nobody to tell and a dead end there is a command that
//	         silently did nothing.

// NewOpts is how `holt new` was asked to finish.
//
// The DEFAULT is to create the lane and print its path, nothing more — a lane is
// a checkout, and what runs in it is the caller's business (`cd "$(holt new)"`,
// the same shape as `holt child`). Opening a session is opt-in: "make me a lane"
// and "make me a lane and become claude inside it" are different asks, and only
// the second one takes the terminal away from you.
type NewOpts struct {
	Agent string // client id; implies Open when set explicitly
	Open  bool   // exec a session in the lane instead of printing its path
	Cmd   string // command to exec in the lane instead of a client
}

// NewCmd parses `holt new [name] [agent] [--open [agent]] [--agent id] [--cmd …]`.
//
// The second POSITIONAL is still an agent id, and it still implies --open: that
// is the spelling the rice's spawn bind and every shipped hook config use
// (`holt new <name> codex`), and breaking it would break the headline keybind on
// every machine that hasn't rebuilt yet.
func (e *Env) NewCmd(args []string) error {
	var name string
	var opts NewOpts
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--open":
			opts.Open = true
			// `--open codex` reads naturally and is what people type.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && registry.KnownAgent(args[i+1]) {
				i++
				opts.Agent = args[i]
			}
		case "--agent":
			if i+1 >= len(args) {
				return exitcode.Usagef("--agent needs a client id (claude, codex, opencode)")
			}
			i++
			opts.Agent, opts.Open = args[i], true
		case "--cmd":
			if i+1 >= len(args) {
				return exitcode.Usagef("--cmd needs a command to run in the lane")
			}
			i++
			opts.Cmd = args[i]
		default:
			switch {
			case strings.HasPrefix(a, "--agent="):
				opts.Agent, opts.Open = a[len("--agent="):], true
			case strings.HasPrefix(a, "--cmd="):
				opts.Cmd = a[len("--cmd="):]
			case strings.HasPrefix(a, "-"):
				return exitcode.Usagef("unknown flag %q — try `holt --help`", a)
			case name == "":
				name = a
			case opts.Agent == "":
				opts.Agent, opts.Open = a, true
			default:
				return exitcode.Usagef("usage: holt new [name] [--open [agent]] [--cmd '<command>']")
			}
		}
	}
	if opts.Cmd != "" && opts.Open {
		return exitcode.Usagef("--cmd and --open/--agent do different things with the same pane — pick one")
	}
	return e.New(name, opts)
}

// New opens a lane on THIS repo.
//
// This is the client-agnostic spawn bind. Claude Code has a native --worktree
// flag that fires the create hook; Codex and OpenCode have nothing like it, so a
// machine whose default is one of those had a headline keybind that launched a
// client it may not even have installed.
func (e *Env) New(want string, opts NewOpts) error {
	agentID := orDefault(opts.Agent, e.Agent)
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
	trustWorktree(agentID, main, dir)
	ui.Say("created %s lane '%s' → %s", filepath.Base(main), name, dir)

	// The default ending: ONLY the path on stdout, so: cd "$(holt new)".
	if !opts.Open && opts.Cmd == "" {
		ui.Out("%s\n", dir)
		return nil
	}

	// --cmd is the escape hatch for anything that isn't a client holt knows: a
	// multiplexer pane, a shell, a build. It skips the open hook, because the
	// caller has already said exactly what to run.
	if opts.Cmd != "" {
		if err := os.Chdir(dir); err != nil {
			return exitcode.Usagef("could not enter %s", dir)
		}
		return execClient([]string{shellOf(), "-c", opts.Cmd})
	}

	// How a fresh lane gets its session is the machine's business, same as
	// `resume`. holt's own answer is to become the client; a machine with a
	// multiplexer would rather have a pane.
	entry := Entry{Main: main, Branch: "worktree-" + name, Path: dir, State: Live}
	// Resolved without the install check that resolveAgent does below: the hook
	// is told what holt WOULD run, and an uninstalled client is that hook's
	// problem to report, not a reason to withhold the command.
	openSpec, _ := specFor(agentID)
	if res := e.openSession(config.HookOpen, entry, agentID, dir, openSpec.open); res.Answer != config.Defer {
		return hookOutcome(config.HookOpen, res)
	}

	// The client is resolved LAST, and its absence is not fatal to the lane:
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

// Child opens a lane on ANOTHER repo, as a child of this pane.
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

	// Default the child's name to THIS pane's own lane name, so a child lane
	// shares its parent's identity (…-sparkle in both repos).
	if want == "" {
		if b := gitx.CurrentBranch(e.Cwd); len(b) > 9 && b[:9] == "worktree-" {
			want = b[9:]
		} else {
			want = filepath.Base(e.Cwd)
		}
	}

	dir := filepath.Join(e.Base, e.bucketFor(main), want)
	if _, err := os.Stat(dir); err == nil {
		return exitcode.Usagef("a lane already exists at %s — pass another name: holt child %s <name>", dir, target)
	}
	if gitx.HasBranch(main, "worktree-"+want) {
		return exitcode.Usagef("branch worktree-%s already exists in %s — pass another name: holt child %s <name>",
			want, filepath.Base(main), target)
	}
	if err := e.addWorktree(main, want, dir); err != nil {
		return err
	}
	// Registered with THIS pane's cwd as parent — the same field the create hook
	// stores — so the statusline lists the child under the lane that spawned it,
	// and queries the CHILD repo's forge for its PR state.
	agentID := e.agentForPath(e.Cwd)
	_ = e.Reg.Put(registry.Row{
		Name: want, Main: main, Branch: "worktree-" + want,
		Path: dir, Parent: e.Cwd, Agent: agentID,
	})
	trustWorktree(agentID, main, dir)
	ui.Say("created %s lane '%s' → %s", filepath.Base(main), want, dir)
	ui.Out("%s\n", dir) // ONLY the path on stdout, so: cd "$(holt child …)"
	return nil
}

// Spawn opens a NAMED lane for a spawner that has no pane of its own.
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
	trustWorktree(agentID, main, dir)
	ui.Say("created %s lane '%s' → %s", filepath.Base(main), name, dir)
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
// own repo basename (the nested case: a workshop named `haus` holding a
// rice also named `haus`) — then the full owner-repo slug, so the child
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
		"cozy", "plucky", "snug", "spry", "zippy", "dozy", "bouncy", "chipper",
		"wiggly", "sunny", "peppy", "drowsy", "giddy", "mellow", "perky", "cuddly",
		"jaunty", "breezy", "fuzzy", "merry", "scrappy", "tiny", "dapper", "silly",
		"cheeky", "nimble", "sleepy", "jolly", "spunky", "tidy", "wobbly", "snappy",
		"dainty", "husky", "plump", "sprightly", "frisky", "pudgy", "gentle", "nifty",
	}
	nouns := []string{
		"otter", "heron", "puffin", "badger", "wombat", "gecko", "finch", "marmot",
		"weasel", "lemur", "vole", "shrew", "mole", "hedgehog", "ferret", "stoat",
		"lynx", "mink", "civet", "quokka", "pika", "chipmunk", "squirrel", "raccoon",
		"possum", "beaver", "sparrow", "wren", "robin", "swift", "tern", "plover",
		"kestrel", "falcon", "osprey", "egret", "crane", "stork", "ibis", "loon",
	}
	return adjectives[rand.IntN(len(adjectives))] + "-" + nouns[rand.IntN(len(nouns))]
}
