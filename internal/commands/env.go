package commands

import (
	"os"
	"path/filepath"
	"strings"

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

// configuredAgent reads the one machine-wide setting Holt needs before its
// richer config surface lands. It is deliberately small and dependency-free:
// agent ids have no TOML syntax worth interpreting beyond `agent = "codex"`.
// Unknown and malformed values are ignored, leaving the documented fallbacks
// intact rather than turning every `holt new` into a hard failure.
func configuredAgent() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		if home == "" {
			return ""
		}
		// Holt's documented config is ~/.config on every platform, including
		// macOS, rather than os.UserConfigDir's Application Support location.
		configDir = filepath.Join(home, ".config")
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "holt", "config.toml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "agent" {
			continue
		}
		value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
		value = strings.Trim(value, " \t\"'")
		if registry.KnownAgent(value) {
			return value
		}
	}
	return ""
}

// defaultAgent is the client a new worktree opens in when nothing says
// otherwise. HOLT_AGENT is an explicit per-invocation override; the persisted
// config works for long-running callers such as Zellij and for standalone Holt.
// NEBELHAUS_AGENT_DEFAULT remains a cutover fallback for pre-config rice builds.
func defaultAgent() string {
	if a := os.Getenv("HOLT_AGENT"); registry.KnownAgent(a) {
		return a
	}
	if a := configuredAgent(); a != "" {
		return a
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
	agent := defaultAgent()
	registry.DefaultAgent = agent
	return &Env{Base: base, Reg: reg, Cwd: cwd, Agent: agent}, nil
}

// Warn records a degraded-mode explanation. Every one of these becomes a
// `warnings[]` entry under --json and an exit code of Degraded, because silent
// degradation is how a user learns to distrust the tool (SPEC.md §3.4).
func (e *Env) Warn(msg string) { e.Warnings = append(e.Warnings, msg) }
