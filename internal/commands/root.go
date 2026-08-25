package commands

import (
	"os"
	"strings"

	"github.com/hausfold/holt/internal/exitcode"
)

// Version is stamped at build time (-ldflags "-X …commands.Version=…").
var Version = "0.1.0-dev"

const usage = `holt — the worktree-lifecycle substrate

A LANE is one agent's branch, checkout and pane, from create to reaped.

  holt                    list every live/parked lane, across all repos
  holt <name>             resume one: rebuild its checkout, reopen its agent
                          --pick to choose the session instead of the newest
  holt focus <name>       go to the window a lane is already running in
                          ([hooks] focus; falls back to resume without one)
  holt new [name]         a lane on THIS repo; prints its path: cd "$(holt new)"
                          --open [agent] open a session in it (--agent <id>)
                          --cmd '<command>' run something else in it instead
                          --prompt '<task>' | --prompt-file <file|-> open it on
                          a first turn (implies --open) · --image <file>
  holt child <repo>       a lane on ANOTHER repo, as a child of this pane
  holt spawn <repo> <name>
                          a named lane for a spawner with no pane of its own
                          --prompt/--prompt-file/--image as above, and the lane
                          is opened through [hooks] open — exit 3 if none
                          <name> is optional with a --prompt: set namer = "<id>"
                          in the config and the task names the lane
  holt park [label]       set the working tree aside as a wip: commit on this branch
  holt unpark             put the last parked commit's changes back, uncommitted
  holt reap               sweep every LANDED lane that nobody is standing in
  holt reaped             what holt has reaped, why, and the SHA to get it back
  holt drop <name>        retire a lane whose work will never land (closed PR,
                          archived repo) — recorded in holt reaped, undoable
  holt heartbeat [path]   hold the occupancy lease on a lane, so reap spares it
                          --pid N (0 = TTL-only) · --release to drop it
  holt watch --json       lifecycle events on stdout, one NDJSON object per line
  holt reship [name]      push a branch that outran its merged PR, open the follow-up
  holt runtime up <name>  stand up a lane's runtime-isolation backend
                          --backend <id> (required — never automatic)
                          tart is built in: a headless macOS per lane
  holt runtime enter <name> --backend <id>
                          drop into it interactively
  holt runtime down <name> --backend <id>
                          tear it down
  holt runtime eject tart print the built-in backend as an adapter file to edit
  holt hook create        [hook] open a lane — JSON on stdin, path on stdout
  holt hook remove        [hook] retire one without losing work — JSON on stdin
  holt hook notify        [hook] client events → a trill banner for the lane:
                          Notification hangs an ask, Stop replaces it with a
                          done, UserPromptSubmit/PostToolUse resolve it —
                          JSON on stdin, exit 0 always, no-op without trill

  --json                  machine-readable listing: holt --json, holt list --json
  --version               print the version

Exit codes: 0 ok · 1 usage · 2 refused for safety · 3 degraded · 4 conflict found
            5 registry locked
`

// Run dispatches one invocation and returns the error to exit on.
func Run(args []string) error {
	// Before anything else: holt is invoked by hooks that supply no PATH, and it
	// resolves git for every single operation. See rescuePATH.
	rescuePATH()

	env, err := NewEnv()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return env.List(false)
	}

	switch args[0] {
	case "-h", "--help", "help":
		os.Stderr.WriteString(usage)
		return nil

	case "--version", "version":
		os.Stdout.WriteString(Version + "\n")
		return nil

	case "park":
		return env.Park(argAt(args, 1))

	case "unpark":
		return env.Unpark()

	case "list":
		return env.List(hasFlag(args, "--json"))

	// `holt --json` is `holt list --json`. Bare `holt` IS the listing, so its
	// machine-readable form has to be spellable without naming the implied verb
	// — the statusline runs it several times a minute and every consumer that
	// reached for the obvious spelling got "unknown flag" instead.
	case "--json":
		return env.List(true)

	case "reap":
		return env.Reap()

	case "reaped":
		return env.Ledger()

	case "drop":
		return env.Drop(argAt(args, 1))

	case "heartbeat":
		return env.Heartbeat(args[1:])

	case "watch":
		return env.Watch(args[1:])

	case "resume":
		return env.Resume(firstBare(args[1:]), hasFlag(args, "--pick"))

	// `holt focus` is `holt <name>` minus the reopening: go to the window the
	// lane is already running in. It is typed rarely and clicked often — it is
	// what trill runs when a lane's banner is clicked.
	case "focus":
		return env.Focus(firstBare(args[1:]))

	case "new":
		return env.NewCmd(args[1:])

	case "child":
		return env.Child(argAt(args, 1), argAt(args, 2))

	case "spawn":
		return env.SpawnCmd(args[1:])

	case "agent":
		return env.AgentCmd(args[1:])

	case "reship":
		return env.Reship(argAt(args, 1))

	case "runtime":
		return env.RuntimeCmd(args[1:])

	// `holt hook create` is the documented spelling. The bare `create` /
	// `remove` verbs are kept because that is what the shipped Claude Code hook
	// configuration calls today, and cutover must not require editing both the
	// hook config and the binary in the same breath (SPEC.md §10).
	case "hook":
		switch argAt(args, 1) {
		case "create":
			return env.HookCreate(os.Stdin)
		case "remove":
			return env.HookRemove(os.Stdin)
		case "notify":
			return env.HookNotify(os.Stdin)
		default:
			return exitcode.Usagef("usage: holt hook <create|remove|notify>  (JSON on stdin)")
		}
	case "create":
		return env.HookCreate(os.Stdin)
	case "remove":
		return env.HookRemove(os.Stdin)

	default:
		// A bare token is a lane name: `holt sparkle` resumes it. This is the
		// spelling that gets typed, so an unknown one must fail the way resume
		// does — naming the listing — not with a generic "unknown command".
		if strings.HasPrefix(args[0], "-") {
			return exitcode.Usagef("unknown flag %q — try `holt --help`", args[0])
		}
		return env.Resume(args[0], hasFlag(args, "--pick"))
	}
}

// firstBare is the first argument that isn't a flag — `holt resume --pick back`
// and `holt resume back --pick` have to mean the same thing, because both get
// typed.
func firstBare(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
