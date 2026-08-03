package commands

import (
	"os"
	"path/filepath"

	"github.com/nebelhaus/holt/internal/registry"
)

// Env is the resolved environment one holt invocation runs in.
type Env struct {
	Base     string // where checkouts live
	Reg      *registry.Registry
	Cwd      string
	Agent    string // the default client for new worktrees
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

// defaultAgent is the client a new worktree opens in when nothing says
// otherwise. NEBELHAUS_AGENT_DEFAULT is read for cutover compatibility; HOLT_AGENT
// is the name holt documents.
func defaultAgent() string {
	for _, key := range []string{"HOLT_AGENT", "NEBELHAUS_AGENT_DEFAULT"} {
		if a := os.Getenv(key); a != "" && registry.KnownAgent(a) {
			return a
		}
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
	agent := defaultAgent()
	registry.DefaultAgent = agent
	return &Env{Base: base, Reg: reg, Cwd: cwd, Agent: agent}, nil
}

// Warn records a degraded-mode explanation. Every one of these becomes a
// `warnings[]` entry under --json and an exit code of Degraded, because silent
// degradation is how a user learns to distrust the tool (SPEC.md §3.4).
func (e *Env) Warn(msg string) { e.Warnings = append(e.Warnings, msg) }
