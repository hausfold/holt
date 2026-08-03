package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/registry"
	"github.com/nebelhaus/holt/internal/ui"
)

// This is the ONE client-specific seam in holt, and it is deliberately narrow.
// Every worktree records its client in the registry, so changing the machine's
// default later never makes a parked Codex branch reopen in Claude.
//
// In 0.2 this whole file collapses into adapter TOML (SPEC.md §5.3) — which is
// why the per-client knowledge is concentrated in three small switches rather
// than spread through the commands that call them.

// agentSpec is what holt needs to know about a client. The 0.2 adapter loader
// produces exactly this struct from a TOML file.
type agentSpec struct {
	id     string
	start  func(image, prompt string) []string
	open   []string
	resume []string
	// imageFlag reports whether the client can attach a local image itself.
	// The ones that can't are TOLD about the file in their first turn, rather
	// than pretending an unsupported flag attached it.
	imageFlag bool
}

func specFor(id string) (agentSpec, bool) {
	switch id {
	case "claude":
		return agentSpec{
			id:     "claude",
			start:  func(_, prompt string) []string { return []string{"claude", prompt} },
			open:   []string{"claude"},
			resume: []string{"claude", "--resume"},
		}, true
	case "codex":
		return agentSpec{
			id: "codex",
			start: func(image, prompt string) []string {
				if image != "" {
					return []string{"codex", "-i", image, prompt}
				}
				return []string{"codex", prompt}
			},
			open:      []string{"codex"},
			resume:    []string{"codex", "resume"},
			imageFlag: true,
		}, true
	case "opencode":
		return agentSpec{
			id:     "opencode",
			start:  func(_, prompt string) []string { return []string{"opencode", "--prompt", prompt} },
			open:   []string{"opencode"},
			resume: []string{"opencode", "--continue"},
		}, true
	}
	return agentSpec{}, false
}

func resolveAgent(id string) (agentSpec, error) {
	spec, ok := specFor(id)
	if !ok {
		return spec, exitcode.Usagef("unknown agent %q (expected claude, codex, or opencode)", id)
	}
	if _, err := exec.LookPath(id); err != nil {
		return spec, exitcode.Usagef("%s is unavailable — install it, then try again", id)
	}
	return spec, nil
}

// execClient replaces this process with the client.
//
// A real exec, not a child: holt IS the pane's process, so closing the client
// closes the pane — and under the rice's binds that fires the same remove hook
// Claude's own exit does. A child process would leave holt sitting in the middle,
// and the pane would outlive the session.
func execClient(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return exitcode.Usagef("%s is unavailable — install it, then try again", argv[0])
	}
	return syscall.Exec(path, argv, os.Environ())
}

// AgentCmd is the public client seam: `holt agent <default|start|open|resume> …`.
func (e *Env) AgentCmd(args []string) error {
	switch argAt(args, 0) {
	case "default":
		ui.Out("%s\n", e.Agent)
		return nil
	case "start":
		return e.agentStart(args[1:])
	case "open":
		spec, err := resolveAgent(orDefault(argAt(args, 1), e.Agent))
		if err != nil {
			return err
		}
		return execClient(spec.open)
	case "resume":
		spec, err := resolveAgent(orDefault(argAt(args, 1), e.Agent))
		if err != nil {
			return err
		}
		return execClient(spec.resume)
	default:
		return exitcode.Usagef("usage: holt agent <default|start|open|resume> …")
	}
}

// agentStart parses `[<agent>] [--image FILE] -- <prompt>` and execs the client.
func (e *Env) agentStart(args []string) error {
	id := e.Agent
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && args[0] != "--" {
		id, args = args[0], args[1:]
	}
	var image string
	if len(args) >= 2 && args[0] == "--image" {
		image, args = args[1], args[2:]
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	prompt := strings.Join(args, " ")

	spec, err := resolveAgent(id)
	if err != nil {
		return err
	}
	if image != "" {
		if _, statErr := os.Stat(image); statErr != nil {
			image = ""
		}
	}
	// A client with no image flag is told where the file is, in words. Silently
	// dropping it would leave the agent reasoning about a screenshot it was
	// never given.
	if image != "" && !spec.imageFlag {
		prompt += "\n\nA screenshot for this task is at " + image +
			". Inspect it before drawing conclusions."
		image = ""
	}
	return execClient(spec.start(image, prompt))
}

// ── where a worktree's conversation lives ────────────────────────────────────

// projDir is Claude Code's transcript directory for a cwd: it encodes the
// project by path, replacing every '/' and '.' with '-'.
func projDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	enc := strings.Map(func(r rune) rune {
		if r == '/' || r == '.' {
			return '-'
		}
		return r
	}, cwd)
	return filepath.Join(home, ".claude", "projects", enc)
}

// agentHasChat answers only when it is knowable.
//
// Clients own their transcript stores, and only Claude exposes a cheap
// cwd → transcript-directory test. Codex and OpenCode keep private session
// indexes, so their cwd-filtered pickers are the authority and holt must not
// guess on their behalf — "unknown" is the honest answer, and the caller
// degrades to opening the picker.
func agentHasChat(agent, cwd string) bool {
	if agent != "claude" {
		return false
	}
	fi, err := os.Stat(projDir(cwd))
	return err == nil && fi.IsDir()
}

// chatHome is the cwd whose client picker should be opened for a worktree.
//
// A SPAWNED worktree never hosts an independent conversation: its chat lives in
// the pane that made it. Two signatures for that, both requiring the parent to
// be a genuinely different context than this worktree's own repo:
//
//  1. the parent is itself an agent worktree — a nested spawn;
//  2. the parent is a checkout of a DIFFERENT repo — a `holt child`, e.g. a
//     workshop pane that spawned this sub-repo worktree.
//
// A plain same-repo worktree's parent is its OWN main checkout, whose
// transcripts are the user's unrelated on-main work. Never hijack resume to
// that — it falls through and the worktree keeps its own chat.
func (e *Env) chatHome(agent, wt string) string {
	if agentHasChat(agent, wt) {
		return wt
	}
	row, ok := e.Reg.Find(wt)
	if !ok || row.Parent == "" {
		return wt
	}
	usable := func(parent string) bool {
		return agent != "claude" || agentHasChat(agent, parent)
	}
	if strings.HasPrefix(row.Parent, e.Base+string(filepath.Separator)) && usable(row.Parent) {
		return row.Parent
	}
	// Cross-repo? Compare the two checkouts' git common dirs — both resolved by
	// git, so symlink-consistent. A raw string compare against the stored path
	// breaks on macOS's /var → /private/var.
	pcommon, perr := gitx.Run(row.Parent, "rev-parse", "--path-format=absolute", "--git-common-dir")
	mcommon, _ := gitx.Run(row.Main, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if perr == nil && pcommon != "" && pcommon != mcommon && usable(row.Parent) {
		return row.Parent
	}
	return wt
}

// agentForPath is the recorded client for a worktree, resolved BEFORE a parked
// checkout is re-registered: a five-column registry row predates the client
// field and is therefore Claude forever, even if the machine's default changed.
func (e *Env) agentForPath(path string) string {
	if row, ok := e.Reg.Find(path); ok && registry.KnownAgent(row.Agent) {
		return row.Agent
	}
	return e.Agent
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
