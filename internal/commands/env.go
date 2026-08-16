package commands

import (
	"os"
	"path/filepath"

	"github.com/hausfold/holt/internal/config"
	"github.com/hausfold/holt/internal/gitx"
	"github.com/hausfold/holt/internal/occupancy"
	"github.com/hausfold/holt/internal/registry"
	"github.com/hausfold/holt/internal/ui"
)

// Env is the resolved environment one holt invocation runs in.
type Env struct {
	Base      string // where checkouts live
	Reg       *registry.Registry
	Cfg       *config.Config // the machine config, and with it the policy seams
	Cwd       string
	Agent     string // the default client for new lanes
	LeaseDir  string // where occupancy leases live
	LeaseSole bool   // leases are the only occupancy signal that can exist here
	Warnings  []string
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

// stateDir is where holt keeps runtime state that is not a checkout.
//
// Deliberately NOT the lane base. Leases are per-process ephemera and the base
// is globbed for checkouts (see discover), so the two must not share a tree.
// The registry stays at $BASE/registry.tsv regardless: cutover day reads the
// file bash `wt` wrote, and no state-dir knob may be able to relocate it
// (SPEC.md §10). Registry v1 is what moves it, once, on purpose.
func stateDir() string {
	if s := os.Getenv("HOLT_STATE"); s != "" {
		return s
	}
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "holt")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "state", "holt")
}

// leasesAreSole reports whether a lease may answer for ABSENCE as well as
// presence — see occupancy.Leases.
//
// HOLT_OCCUPANCY=lease is the embedder's switch: it declares that every session
// in this deployment is one holt spawned, so a lane nobody leased is a lane
// nobody is in. On a developer machine that is false — someone can always cd
// into a checkout without telling holt — which is why the default is the
// cautious one, and why this is opt-in by an explicit env var rather than
// inferred from, say, the absence of lsof.
//
// It is an env var and not a config key on purpose: it describes the
// DEPLOYMENT, not the operator's taste, and the deployment is the thing that
// varies per process rather than per machine. The occupancy question itself
// wants to become a config seam (`occupied`, alongside HookLanded and
// HookPreserve) — that is the shape SPEC.md §14.2's callback lands in, and it
// is one more occupancy.Provider when it does.
func leasesAreSole() bool { return os.Getenv("HOLT_OCCUPANCY") == "lease" }

// defaultAgent is the client a new lane opens in when nothing says
// otherwise.
//
// The ladder, most explicit first: HOLT_AGENT is a one-invocation override; the
// `agent` hook is a program, for a machine that picks per repo or per time of
// day; the `agent` config key is the static answer, which is what almost
// everyone wants and costs no process; HAUS_AGENT_DEFAULT is a cutover
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
	if a := os.Getenv("HAUS_AGENT_DEFAULT"); registry.KnownAgent(a) {
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
	e := &Env{
		Base:      base,
		Reg:       reg,
		Cfg:       cfg,
		Cwd:       cwd,
		LeaseDir:  filepath.Join(stateDir(), "live"),
		LeaseSole: leasesAreSole(),
	}
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

// Occupancy folds every provider this machine offers into one answer.
//
// One scan per invocation, shared across every lane in the sweep. The providers
// compose by union on presence and by "did anyone vouch" on absence;
// occupancy.Collect owns that rule and the reasoning behind it.
func (e *Env) Occupancy() occupancy.Report {
	return occupancy.Collect(
		occupancy.LSOF(),
		occupancy.Leases(e.LeaseDir, e.LeaseSole),
	)
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
