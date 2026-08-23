package commands

import (
	"io"
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
	// Prompt is the lane's FIRST TURN, and it implies Open: a task with no
	// session to hand it to is a task nobody reads. It changes which client
	// invocation gets run — `spec.start`, not `spec.open` — and nothing else
	// about the lane. holt neither stores it nor reads it; it is one argv
	// element passed through, and what the agent then does with it is the
	// agent's business, not holt's.
	Prompt string
	Image  string // a local file the first turn should look at
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
		case "--prompt":
			if i+1 >= len(args) {
				return exitcode.Usagef("--prompt needs the task text (or use --prompt-file)")
			}
			i++
			opts.Prompt, opts.Open = args[i], true
		case "--prompt-file":
			if i+1 >= len(args) {
				return exitcode.Usagef("--prompt-file needs a path, or - for stdin")
			}
			i++
			text, err := readPrompt(args[i])
			if err != nil {
				return err
			}
			opts.Prompt, opts.Open = text, true
		case "--image":
			if i+1 >= len(args) {
				return exitcode.Usagef("--image needs a file path")
			}
			i++
			opts.Image = args[i]
		default:
			switch {
			case strings.HasPrefix(a, "--agent="):
				opts.Agent, opts.Open = a[len("--agent="):], true
			case strings.HasPrefix(a, "--cmd="):
				opts.Cmd = a[len("--cmd="):]
			case strings.HasPrefix(a, "--prompt="):
				opts.Prompt, opts.Open = a[len("--prompt="):], true
			case strings.HasPrefix(a, "--prompt-file="):
				text, err := readPrompt(a[len("--prompt-file="):])
				if err != nil {
					return err
				}
				opts.Prompt, opts.Open = text, true
			case strings.HasPrefix(a, "--image="):
				opts.Image = a[len("--image="):]
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
		return exitcode.Usagef("--cmd and --open/--agent/--prompt do different things with the same pane — pick one")
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
	argv := openSpec.open
	if opts.Prompt != "" {
		argv = startArgv(openSpec, opts.Image, opts.Prompt)
	}
	if res := e.openSession(config.HookOpen, entry, agentID, dir, argv); res.Answer != config.Defer {
		return hookOutcome(config.HookOpen, res)
	}

	// The client is resolved LAST, and its absence is not fatal to the lane:
	// the checkout and the registry row are already on disk, so an uninstalled
	// client costs you this launch, not the branch. `holt <name>` picks it up.
	if _, err := resolveAgent(agentID); err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return exitcode.Usagef("could not enter %s", dir)
	}
	return execClient(argv)
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

// SpawnOpts is how `holt spawn` was asked to finish. Same two fields as
// NewOpts' prompt half, and they mean the same thing — see there.
type SpawnOpts struct {
	Agent  string
	Prompt string
	Image  string
}

// SpawnCmd parses `holt spawn <repo> <name> [agent] [--agent id]
// [--prompt TEXT | --prompt-file FILE] [--image FILE]`.
//
// The third POSITIONAL is still an agent id: that is the spelling every shipped
// palette command uses (`holt spawn "$repo" "$slug" "$agent"`), and it predates
// the flags.
func (e *Env) SpawnCmd(args []string) error {
	var target, want string
	var opts SpawnOpts
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--agent":
			if i+1 >= len(args) {
				return exitcode.Usagef("--agent needs a client id (claude, codex, opencode)")
			}
			i++
			opts.Agent = args[i]
		case "--prompt":
			if i+1 >= len(args) {
				return exitcode.Usagef("--prompt needs the task text (or use --prompt-file)")
			}
			i++
			opts.Prompt = args[i]
		case "--prompt-file":
			if i+1 >= len(args) {
				return exitcode.Usagef("--prompt-file needs a path, or - for stdin")
			}
			i++
			text, err := readPrompt(args[i])
			if err != nil {
				return err
			}
			opts.Prompt = text
		case "--image":
			if i+1 >= len(args) {
				return exitcode.Usagef("--image needs a file path")
			}
			i++
			opts.Image = args[i]
		default:
			switch {
			case strings.HasPrefix(a, "--agent="):
				opts.Agent = a[len("--agent="):]
			case strings.HasPrefix(a, "--prompt="):
				opts.Prompt = a[len("--prompt="):]
			case strings.HasPrefix(a, "--prompt-file="):
				text, err := readPrompt(a[len("--prompt-file="):])
				if err != nil {
					return err
				}
				opts.Prompt = text
			case strings.HasPrefix(a, "--image="):
				opts.Image = a[len("--image="):]
			case strings.HasPrefix(a, "-"):
				return exitcode.Usagef("unknown flag %q — try `holt --help`", a)
			case target == "":
				target = a
			case want == "":
				want = a
			case opts.Agent == "":
				opts.Agent = a
			default:
				return exitcode.Usagef("usage: holt spawn <repo> <name> [--prompt '<task>' | --prompt-file <file>]")
			}
		}
	}
	return e.Spawn(target, want, opts)
}

// Spawn opens a NAMED lane for a spawner that has no pane of its own.
//
// With --prompt it also OPENS that lane, through the same `[hooks] open` seam
// `new` uses — which is the whole difference between the two endings. A spawner
// with no pane cannot exec a client (there is no terminal to become), so the
// seam is not an optimisation here, it is the only way a window happens at all.
// Without a seam configured, the lane is still created and the invocation is
// still reported; what is missing is somewhere to run it, and that is a
// degraded run, not a failure — see below.
func (e *Env) Spawn(target, want string, opts SpawnOpts) error {
	if target == "" || want == "" {
		return exitcode.Usagef("usage: holt spawn <repo-path> <name>")
	}
	agentID := orDefault(opts.Agent, e.Agent)
	spec, ok := specFor(agentID)
	if !ok {
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
	// The path goes to stdout BEFORE the seam runs, and unconditionally: a
	// caller reading `dir="$(holt spawn …)"` still gets one, and a caller that
	// asked for a window still needs to be told where the lane landed in order
	// to report it. The seam's own exit status is what says whether the window
	// happened.
	ui.Out("%s\n", dir)
	if opts.Prompt == "" {
		return nil
	}

	entry := Entry{Main: main, Branch: "worktree-" + name, Path: dir, State: Live}
	// HOLT_CHAT is the lane's own checkout: a spawn continues nothing, so there
	// is no parent pane whose transcript this belongs to.
	argv := startArgv(spec, opts.Image, opts.Prompt)
	if res := e.openSession(config.HookOpen, entry, agentID, dir, argv); res.Answer != config.Defer {
		return hookOutcome(config.HookOpen, res)
	}
	// No seam. Deliberately NOT holt's `new` fallback of exec'ing the client:
	// `spawn` is the "nobody's pane" verb, so the process holt would replace
	// belongs to a script, a palette command or another agent's tool call — all
	// of which would hang on a client waiting for a terminal that isn't there.
	// Invariant 1 still holds: the lane exists and the branch is on disk. What
	// is unavailable is a place to open it, which is exactly exit 3.
	ui.Warn("no [hooks] open configured — the lane is ready, but nothing opened it")
	return exitcode.Degradedf("lane '%s' created at %s; run this inside it: %s",
		name, dir, shellCommand(argv))
}

// ── shared plumbing ──────────────────────────────────────────────────────────

// readPrompt loads a first-turn task from a file, or from stdin for "-".
//
// It exists because the interesting prompts are the long ones. A brief written
// for a cold session is multi-line and routinely contains quotes, backticks and
// `$` — text that survives a Go argv untouched but has to cross a shell to get
// there, and `--prompt "$(cat brief.md)"` is where it gets mangled. Handing
// over a PATH moves the text out of the command line entirely.
//
// Surrounding whitespace goes (a file ends with a newline; that newline is not
// part of the task), and an empty file is a usage error rather than a lane that
// opens on nothing — a caller whose heredoc silently produced nothing should
// hear about it here, not by looking at a blank agent pane.
func readPrompt(path string) (string, error) {
	if path == "" {
		return "", exitcode.Usagef("--prompt-file needs a path, or - for stdin")
	}
	var (
		raw []byte
		err error
	)
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return "", exitcode.Usagef("could not read the prompt from %s: %v", path, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", exitcode.Usagef("%s is empty — there is no task to open the lane on", path)
	}
	return text, nil
}

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
