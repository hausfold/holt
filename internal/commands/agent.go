package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hausfold/holt/internal/exitcode"
	"github.com/hausfold/holt/internal/gitx"
	"github.com/hausfold/holt/internal/registry"
	"github.com/hausfold/holt/internal/ui"
)

// This is the ONE client-specific seam in holt, and it is deliberately narrow.
// Every lane records its client in the registry, so changing the machine's
// default later never makes a parked Codex branch reopen in Claude.
//
// In 0.2 this whole file collapses into adapter TOML (SPEC.md §5.3) — which is
// why the per-client knowledge is concentrated in three small switches rather
// than spread through the commands that call them.

// ── the prompt is DATA, and every client's parser has to be told so ──────────
//
// A task typed into Pounce's Spawn Agent box is very often a markdown list, so
// its first character is `-`. Handed to a client as a bare argv element, that is
// a FLAG: `claude "- update the README"` dies with `error: unknown option '-
// update the README'` before the pane has drawn anything, and the same is true
// of any prompt starting with a dash. So every client's argv here ends its
// option parsing before the prompt — `--` for the two positional-prompt clients
// (commander and clap both honour it), and `--prompt=<text>` for opencode, whose
// yargs would read a dashed VALUE after a separate `--prompt` as another flag.
// Never go back to appending the prompt bare.

// agentSpec is what holt needs to know about a client. The 0.2 adapter loader
// produces exactly this struct from a TOML file.
type agentSpec struct {
	id    string
	start func(image, prompt string) []string
	open  []string
	// resume opens the client's session PICKER, filtered to the cwd. It is the
	// right answer only when holt genuinely cannot tell which conversation is
	// meant — a lane whose chat lives in a shared parent checkout.
	resume []string
	// last continues the newest conversation in the cwd, with no picker. A
	// lane's own checkout is a directory only that lane's agent ever ran in, so
	// "the newest conversation here" IS the lane's chat — asking which one is a
	// question with one answer, and answering it for the user is the point of
	// `holt <name>`. Empty means the client has no such mode and the picker
	// stands.
	last []string
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
			start:  func(_, prompt string) []string { return []string{"claude", "--", prompt} },
			open:   []string{"claude"},
			resume: []string{"claude", "--resume"},
			last:   []string{"claude", "--continue"},
		}, true
	case "codex":
		return agentSpec{
			id: "codex",
			start: func(image, prompt string) []string {
				if image != "" {
					return []string{"codex", "-i", image, "--", prompt}
				}
				return []string{"codex", "--", prompt}
			},
			open:      []string{"codex"},
			resume:    []string{"codex", "resume"},
			last:      []string{"codex", "resume", "--last"},
			imageFlag: true,
		}, true
	case "opencode":
		return agentSpec{
			id:    "opencode",
			start: func(_, prompt string) []string { return []string{"opencode", "--prompt=" + prompt} },
			open:  []string{"opencode"},
			// opencode's `--continue` is already continue-the-last-session; it
			// has no separate picker flag (its TUI lists sessions in-app), so
			// both rungs are the same command.
			resume: []string{"opencode", "--continue"},
			last:   []string{"opencode", "--continue"},
		}, true
	case "jcode":
		return agentSpec{
			id: "jcode",
			// The one client with NO way to be handed a first message: no
			// positional prompt, no --prompt, and it does not read stdin
			// (checked against v0.76.0 — `jcode run "<msg>"` is the
			// non-interactive path and exits, which is not a pane). So the
			// prompt is dropped HERE, deliberately, and the caller is the one
			// that has to say so: haus's Spawn Agent copies it to the clipboard
			// and tells you. A lane is still worth spawning without it — the
			// worktree, the branch named after the task and the pane are all
			// still right.
			start: func(_, _ string) []string { return []string{"jcode"} },
			open:  []string{"jcode"},
			// `--resume` with no id opens jcode's session picker. There is no
			// continue-the-newest flag, so both rungs are the same command, as
			// with opencode — the picker in a lane's own checkout lists that
			// lane's conversations and little else.
			resume: []string{"jcode", "--resume"},
			last:   []string{"jcode", "--resume"},
		}, true
	}
	return agentSpec{}, false
}

// resumeArgv picks between continuing the newest conversation and opening the
// picker.
//
// `own` says the lane's chat lives in the lane's OWN checkout. `pick` is the
// user overriding from the command line, for the case holt's rule gets wrong:
// a lane whose newest conversation is not the one wanted (a throwaway session
// started in the same checkout, or a deliberate second thread).
func resumeArgv(spec agentSpec, own, pick bool) []string {
	if own && !pick && len(spec.last) > 0 {
		return spec.last
	}
	return spec.resume
}

func resolveAgent(id string) (agentSpec, error) {
	spec, ok := specFor(id)
	if !ok {
		return spec, exitcode.Usagef("unknown agent %q (expected claude, codex, opencode, or jcode)", id)
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
// and the pane would outlive the client.
func execClient(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return exitcode.Usagef("%s is unavailable — install it, then try again", argv[0])
	}
	return syscall.Exec(path, argv, os.Environ())
}

// shellOf is the shell `holt new --cmd` runs a command string through: the
// user's own $SHELL when they have one, /bin/sh otherwise. Their shell, because
// the command was typed by them and may lean on their aliases and functions.
func shellOf() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
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

// ── inheriting the parent repo's workspace trust ─────────────────────────────

// trustWorktree stops a freshly-made worktree greeting its first Claude session
// with "Do you trust the files in this folder?".
//
// Claude Code keys workspace trust on the EXACT cwd, in `~/.claude.json` under
// `projects["<abs path>"].hasTrustDialogAccepted`. There is no inheritance from a
// parent directory and none from the git common dir — so a checkout holt just
// made is, correctly, a directory Claude has never seen. Claude's own
// `--worktree` doesn't prompt because it seeds that key for the worktree it
// creates; every checkout holt makes instead (the palette's `holt spawn`,
// `holt new` on a claude machine, `holt child`) got the dialog. Same worktree,
// same repo, different answer depending on who ran `git worktree add` — which
// reads as a bug in the spawn, because it is one.
//
// Deliberately narrow, in three ways:
//
//   - It only ever COPIES a decision the user already made. If the parent repo
//     isn't trusted, this is a no-op — holt never grants trust on the user's
//     behalf, it propagates it to a checkout of the same code.
//   - Every failure is silent and harmless. A missing/unreadable/unparseable
//     `~/.claude.json` costs one trust prompt, which is exactly the status quo;
//     nothing here is worth failing a spawn over.
//   - It is a no-op for every other client. Codex and OpenCode have no
//     equivalent, and holt must not invent one.
//
// The write is read-modify-write on a file Claude Code also owns and rewrites
// wholesale, with no lock either side — so a Claude instance writing in the same
// instant can drop this key. The blast radius is one trust prompt, because
// everything else in that file is Claude's own telemetry which it is in the
// middle of rewriting anyway. Marshalling through a map also reorders the file's
// keys once (Go maps have no order); numbers are decoded as json.Number so the
// re-encode can't turn `1778838900185` into `1.778838900185e+12`.
func trustWorktree(agentID, main, dir string) {
	if agentID != "claude" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return
	}
	projects, _ := doc["projects"].(map[string]any)
	if projects == nil {
		return
	}
	parent, _ := projects[main].(map[string]any)
	if trusted, _ := parent["hasTrustDialogAccepted"].(bool); !trusted {
		return
	}
	entry, _ := projects[dir].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	if already, _ := entry["hasTrustDialogAccepted"].(bool); already {
		return // nothing to write; don't churn a 180KB file for nothing
	}
	entry["hasTrustDialogAccepted"] = true
	projects[dir] = entry

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a path or prompt with < > & stays readable
	enc.SetIndent("", "  ")  // what Claude Code itself writes
	if err := enc.Encode(doc); err != nil {
		return
	}
	// Temp file in the same directory + rename, so a crash mid-write can never
	// leave Claude with a truncated config. 0600 because this file holds
	// credentials, and CreateTemp's own 0600 is what we keep.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.holt-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}

// ── where a lane's conversation lives ────────────────────────────────────────

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

// chatHome is the cwd whose client picker should be opened for a lane.
//
// A SPAWNED lane never hosts an independent conversation: its chat lives in the
// pane that made it. Two signatures for that, both requiring the parent to be a
// genuinely different context than this lane's own repo:
//
//  1. the parent is itself a lane — a nested spawn;
//  2. the parent is a checkout of a DIFFERENT repo — a `holt child`, e.g. a
//     workshop pane that spawned this sub-repo lane.
//
// A plain same-repo lane's parent is its OWN main checkout, whose transcripts
// are the user's unrelated on-main work. Never hijack resume to that — it falls
// through and the lane keeps its own chat.
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

// agentForPath is the recorded client for a lane, resolved BEFORE a parked
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
