package commands

import (
	"os"
	"path/filepath"

	"github.com/nebelhaus/holt/internal/config"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/registry"
	"github.com/nebelhaus/holt/internal/ui"
)

// Env is the resolved environment one holt invocation runs in.
type Env struct {
	Base     string // where checkouts live
	Reg      *registry.Registry
	Cfg      *config.Config // the machine config, and with it the policy seams
	Cwd      string
	Agent    string // the default client for new lanes
	Warnings []string
}

// baseDir resolves where worktree checkouts live.
//
// CLAUDE_WT_BASE is honoured ahead of HOLT_BASE and the default path still ends
// in `claude-worktrees`, both for the same reason: on cutover day holt must find
// the worktrees the bash `wt` already made (SPEC.md §10). The name is historical
// — every client shares the directory — and renaming it is a migration, not a
// rename, so it waits for registry v1.
func baseDir() string {
	if b := os.Getenv("CLAUDE_WT_BASE"); b != "" {
		return b
	}
	if b := os.Getenv("HOLT_BASE"); b != "" {
		return b
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".cache", "claude-worktrees")
}

// defaultAgent is the client a new lane opens in when nothing says
// otherwise.
//
// The ladder, most explicit first: HOLT_AGENT is a one-invocation override; the
// `agent` hook is a program, for a machine that picks per repo or per time of
// day; the `agent` config key is the static answer, which is what almost
// everyone wants and costs no process; NEBELHAUS_AGENT_DEFAULT is a cutover
// fallback for pre-config rice builds; claude is the last word.
//
// A value that names a client holt has never heard of is ignored at every rung
// rather than fatal — an unknown agent must not turn every `holt new` into a
// hard failure when a working default is one rung down.
func (e *Env) defaultAgent() string {
	if a := os.Getenv("HOLT_AGENT"); registry.KnownAgent(a) {
		return a
	}
	if e.Cfg.Defined(config.HookAgent) {
		res := e.Cfg.Ask(config.HookAgent, map[string]string{"cwd": e.Cwd})
		e.noteHook(res)
		if id, _ := res.Data["agent"].(string); res.Answer == config.Yes && registry.KnownAgent(id) {
			return id
		}
	}
	if registry.KnownAgent(e.Cfg.Agent) {
		return e.Cfg.Agent
	}
	if a := os.Getenv("NEBELHAUS_AGENT_DEFAULT"); registry.KnownAgent(a) {
		return a
	}
	return "claude"
}

// NewEnv resolves the environment for one invocation.
func NewEnv() (*Env, error) {
	base := baseDir()
	reg, err := registry.Open(filepath.Join(base, "registry.tsv"))
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cfg, cfgWarnings := config.Load()
	e := &Env{Base: base, Reg: reg, Cfg: cfg, Cwd: cwd}
	// Through Warn, not straight into the field: a line of the config that
	// didn't parse is a seam the operator believes is in force and isn't, which
	// is the one thing this whole surface must never be quiet about.
	for _, w := range cfgWarnings {
		e.Warn(w)
	}
	e.Agent = e.defaultAgent()
	registry.DefaultAgent = e.Agent
	return e, nil
}

// Warn records a degraded-mode explanation AND says it out loud. Every one of
// these becomes a `warnings[]` entry under --json, because silent degradation is
// how a user learns to distrust the tool (SPEC.md §3.4).
//
// It prints as well as records because recording alone was the same silence with
// extra steps: `warnings[]` is only ever rendered under --json, so a human
// running `holt reap` never saw "no forge CLI on PATH — nothing will be reaped
// on that basis", and now would never see "your landed hook wouldn't run". Both
// go to stderr, which leaves the stdout data contract (SPEC.md §2.3) untouched.
func (e *Env) Warn(msg string) {
	e.Warnings = append(e.Warnings, msg)
	ui.Warn("%s", msg)
}

// noteHook surfaces a hook that misbehaved. A policy override that quietly
// stopped applying is worse than one that never existed, because the operator
// still believes it is in force.
func (e *Env) noteHook(res config.Result) {
	if res.Warning != "" {
		e.Warn(res.Warning)
	}
}

// hookPayload is the situation, in the same names the adapter templates use
// (SPEC.md §5.2). Every hook gets the same table; the empty fields are the
// honest answer for the ones a given seam has no value for.
func (e *Env) hookPayload(main, branch, path, agent string) map[string]string {
	name := branch
	if len(branch) > 9 && branch[:9] == "worktree-" {
		name = branch[9:]
	}
	slug, _ := gitx.RemoteSlug(main)
	payload := map[string]string{
		"path":   path,
		"main":   main,
		"repo":   slug,
		"name":   name,
		"branch": branch,
		"base":   gitx.DefaultBranch(main),
		"agent":  agent,
		"cwd":    e.Cwd,
	}
	if row, ok := e.Reg.Find(path); ok {
		payload["parent"] = row.Parent
	}
	return payload
}
