package commands

import (
	"os"
	"strings"

	"github.com/nebelhaus/holt/internal/exitcode"
)

// Version is stamped at build time (-ldflags "-X …commands.Version=…").
var Version = "0.1.0-dev"

const usage = `holt — the worktree-lifecycle substrate

  holt                    list every live/parked agent worktree, across all repos
  holt <name>             resume one: rebuild its checkout, reopen its agent
  holt new [name]         worktree of THIS repo, then open the default agent in it
  holt child <repo>       worktree of ANOTHER repo, as a child of this pane
  holt spawn <repo> <name>
                          a named worktree for a spawner with no pane of its own
  holt park [label]       set the working tree aside as a wip: commit on this branch
  holt unpark             put the last parked commit's changes back, uncommitted
  holt reap               sweep every LANDED worktree that nobody is standing in
  holt reship [name]      push a branch that outran its merged PR, open the follow-up
  holt hook create        [hook] make a worktree — JSON on stdin, path on stdout
  holt hook remove        [hook] retire one without losing work — JSON on stdin

  --json                  machine-readable output (list, status)
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

	case "reap":
		return env.Reap()

	case "resume":
		return env.Resume(argAt(args, 1))

	case "new":
		return env.New(argAt(args, 1), argAt(args, 2))

	case "child":
		return env.Child(argAt(args, 1), argAt(args, 2))

	case "spawn":
		return env.Spawn(argAt(args, 1), argAt(args, 2), argAt(args, 3))

	case "agent":
		return env.AgentCmd(args[1:])

	case "reship":
		return env.Reship(argAt(args, 1))

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
		default:
			return exitcode.Usagef("usage: holt hook <create|remove>  (JSON on stdin)")
		}
	case "create":
		return env.HookCreate(os.Stdin)
	case "remove":
		return env.HookRemove(os.Stdin)

	default:
		// A bare token is a worktree name: `holt sparkle` resumes it. This is the
		// spelling that gets typed, so an unknown one must fail the way resume
		// does — naming the listing — not with a generic "unknown command".
		if strings.HasPrefix(args[0], "-") {
			return exitcode.Usagef("unknown flag %q — try `holt --help`", args[0])
		}
		return env.Resume(args[0])
	}
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
