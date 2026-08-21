package commands

import (
	"errors"
	"os"
	"os/exec"

	"github.com/hausfold/holt/internal/config"
	"github.com/hausfold/holt/internal/exitcode"
	"github.com/hausfold/holt/internal/ui"
)

// `holt runtime` is the explicit, on-demand half of SPEC.md §5.5: a lane gets
// a runtime-isolation backend (a VM, a container) only when a caller names
// one, never automatically from create or remove. That is a deliberate scope
// cut — not every lane needs a VM, and wiring it into `HookCreate`/
// `HookRemove` would mean building the repo-local `.holt.toml` selector this
// milestone doesn't have yet — so `--backend <id>` is required on every verb.

// RuntimeCmd is `holt runtime <up|enter|down> <name> --backend <id>`.
func (e *Env) RuntimeCmd(args []string) error {
	if len(args) == 0 {
		return exitcode.Usagef("usage: holt runtime <up|enter|down> <name> --backend <id>")
	}
	verb, rest := args[0], args[1:]

	var name, backend string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; a {
		case "--backend":
			i++
			if i >= len(rest) {
				return exitcode.Usagef("holt runtime %s --backend needs an id", verb)
			}
			backend = rest[i]
		default:
			if a == "" {
				continue
			}
			if a[0] == '-' {
				return exitcode.Usagef("unknown flag %q — try `holt --help`", a)
			}
			if name != "" {
				return exitcode.Usagef("holt runtime %s takes at most one lane name", verb)
			}
			name = a
		}
	}
	if name == "" {
		return exitcode.Usagef("name a lane: holt runtime %s <name> --backend <id>", verb)
	}

	switch verb {
	case "up":
		return e.RuntimeUp(name, backend)
	case "enter":
		return e.RuntimeEnter(name, backend)
	case "down":
		return e.RuntimeDown(name, backend)
	default:
		return exitcode.Usagef("usage: holt runtime <up|enter|down> <name> --backend <id>")
	}
}

// RuntimeUp runs a backend's setup command for a lane — cloning/booting a VM,
// starting a container, whatever the adapter's `setup` argv does.
//
// stdout/stderr are inherited rather than captured, so a `tart clone`/`tart
// run` (or any other backend) prints its progress live, the same visibility
// Reship gives `gh pr create`.
func (e *Env) RuntimeUp(name, backend string) error {
	entry, vars, adapter, err := e.resolveRuntime(name, backend)
	if err != nil {
		return err
	}
	argv, err := config.RenderArgv(adapter.Setup, vars)
	if err != nil {
		return exitcode.Usagef("rendering %s's setup command: %v", backend, err)
	}
	if len(argv) == 0 {
		return exitcode.Refusedf("the %q runtime adapter declares no setup command", backend)
	}
	ui.Say("standing up %s for %s …", backend, entry.Name())
	if err := runInherited(argv); err != nil {
		return runtimeCommandError(backend, "setup", entry.Name(), argv[0], err)
	}
	return nil
}

// RuntimeEnter drops into a lane's already-provisioned runtime backend.
//
// Exec-replace, the same chdir-and-exec shape HookResume/HookOpen already use
// (config.go's doc comments) — holt has nothing left to do once the session
// starts, an interactive SSH/exec session is exactly the sort of thing that
// should own the terminal directly, and there is no cleanup to run after: the
// backend keeps running until a separate `holt runtime down`.
func (e *Env) RuntimeEnter(name, backend string) error {
	entry, vars, adapter, err := e.resolveRuntime(name, backend)
	if err != nil {
		return err
	}
	argv, err := config.RenderArgv(adapter.Enter, vars)
	if err != nil {
		return exitcode.Usagef("rendering %s's enter command: %v", backend, err)
	}
	if len(argv) == 0 {
		return exitcode.Refusedf("the %q runtime adapter declares no enter command", backend)
	}
	ui.Say("entering %s's %s …", entry.Name(), backend)
	return execClient(argv)
}

// RuntimeDown tears a lane's runtime backend down.
func (e *Env) RuntimeDown(name, backend string) error {
	entry, vars, adapter, err := e.resolveRuntime(name, backend)
	if err != nil {
		return err
	}
	argv, err := config.RenderArgv(adapter.Teardown, vars)
	if err != nil {
		return exitcode.Usagef("rendering %s's teardown command: %v", backend, err)
	}
	if len(argv) == 0 {
		return exitcode.Refusedf("the %q runtime adapter declares no teardown command", backend)
	}
	ui.Say("tearing down %s's %s …", entry.Name(), backend)
	if err := runInherited(argv); err != nil {
		return runtimeCommandError(backend, "teardown", entry.Name(), argv[0], err)
	}
	return nil
}

// resolveRuntime is the lookup every runtime verb starts from: the lane (via
// the same matchLane resolver `holt <name>` and `holt drop` already use, so
// `<repo>/<name>` disambiguation stays in one place), the adapter, and the
// template variables built from it.
func (e *Env) resolveRuntime(name, backend string) (Entry, config.TemplateVars, *config.RuntimeAdapter, error) {
	if backend == "" {
		return Entry{}, config.TemplateVars{}, nil, exitcode.Usagef("name a backend: holt runtime up|enter|down %s --backend <id>", name)
	}
	entry, err := e.matchLane(name)
	if err != nil {
		return Entry{}, config.TemplateVars{}, nil, err
	}
	adapter, err := config.LoadRuntimeAdapter(backend)
	if err != nil {
		return Entry{}, config.TemplateVars{}, nil, exitcode.Refusedf("%v", err)
	}
	return entry, e.templateVars(entry, e.agentForPath(entry.Path)), adapter, nil
}

// templateVars builds SPEC.md §5.2's shared variable set for a lane, reusing
// hookPayload's exact field derivation (name = branch minus the
// `worktree-` prefix, repo via gitx.RemoteSlug, base via gitx.DefaultBranch)
// rather than re-deriving it a second way. Prompt/Image/Port/Env stay
// zero-valued: they are part of the shared set for template compatibility
// with the other adapter kinds, but nothing a runtime command does has a
// value for them.
func (e *Env) templateVars(entry Entry, agent string) config.TemplateVars {
	payload := e.hookPayload(entry.Main, entry.Branch, entry.Path, agent)
	return config.TemplateVars{
		Path:   payload["path"],
		Main:   payload["main"],
		Repo:   payload["repo"],
		Name:   payload["name"],
		Branch: payload["branch"],
		Base:   payload["base"],
		Parent: payload["parent"],
		Agent:  payload["agent"],
	}
}

// runInherited runs argv with the terminal inherited — the setup/teardown
// commands are visible, interactive-capable child processes, not captured
// output holt parses.
func runInherited(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// runtimeCommandError turns a failed setup/teardown command into holt's exit
// code, and the two failure shapes mean different things.
//
// A binary that isn't on PATH at all is the same "a signal was unavailable"
// shape as Reship's "gh is unavailable" case — SPEC.md §2.4's own words for
// exit 3 — so it degrades: install the backend's CLI and the same command
// works. A binary that WAS found and ran and still exited non-zero actually
// attempted the operation and failed at it (a VM that already exists, a full
// disk, a bad image) — that is not "completed, but a signal was unavailable",
// so it lands in Usage instead, the bucket every other "fix this, then
// retry" failure lands in.
func runtimeCommandError(backend, step, lane, bin string, err error) error {
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return exitcode.Degradedf("%s's %s command (%s) is unavailable — install it, then try again", backend, step, bin)
	}
	return exitcode.Usagef("%s's %s failed for %s: %v", backend, step, lane, err)
}
