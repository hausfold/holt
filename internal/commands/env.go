package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hausfold/scruff/internal/config"
	"github.com/hausfold/scruff/internal/gitx"
	"github.com/hausfold/scruff/internal/occupancy"
	"github.com/hausfold/scruff/internal/registry"
	"github.com/hausfold/scruff/internal/ui"
)

// Env is the resolved environment one scruff invocation runs in.
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
// The env ladder is SCRUFF_BASE, then CLAUDE_WT_BASE — the plan-of-record
// order at 1.1.0 (docs/rename.md §8.2): the old spelling's rung is gone, and
// CLAUDE_WT_BASE survives as the last explicit rung because SPEC.md §10's bash
// predecessor is still the reason it exists. It predates both spellings and
// answers to neither.
//
// The default path prefers ~/.cache/scruff and falls back to
// ~/.cache/claude-worktrees only when that is the one holding a
// registry.tsv — the permanent fallback that keeps the base move (§8.2) a
// minor rather than a major: no one who skips `scruff doctor
// --migrate-base` is broken by it. A bare directory is not a base (the
// migration and a fresh install both create the scruff-named one), so the
// fallback keys on the registry, the same source of truth everything else
// reads.
func baseDir() string {
	if b := os.Getenv("SCRUFF_BASE"); b != "" {
		return b
	}
	if b := os.Getenv("CLAUDE_WT_BASE"); b != "" {
		return b
	}
	newBase, oldBase := defaultBaseCandidates()
	if _, err := os.Stat(filepath.Join(newBase, "registry.tsv")); err == nil {
		return newBase
	}
	if _, err := os.Stat(filepath.Join(oldBase, "registry.tsv")); err == nil {
		return oldBase
	}
	return newBase
}

// defaultBaseCandidates is the two default paths the base decision is between:
// the scruff-named base, and the legacy one the bash predecessor and every
// 1.0.x release created. Shared with `doctor --migrate-base`, whose whole job
// is moving the second onto the first.
func defaultBaseCandidates() (scruff, legacy string) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".cache", "scruff"),
		filepath.Join(home, ".cache", "claude-worktrees")
}

// stateDir is where scruff keeps runtime state that is not a checkout.
//
// Deliberately NOT the lane base. Leases are per-process ephemera and the base
// is globbed for checkouts (see discover), so the two must not share a tree.
// The registry stays at $BASE/registry.tsv regardless: cutover day reads the
// file bash `wt` wrote, and no state-dir knob may be able to relocate it
// (SPEC.md §10). Registry v1 is what moves it, once, on purpose.
func stateDir() string {
	dir, _ := resolveStateDir()
	return dir
}

// resolveStateDir is stateDir plus the reason it ignored an override, so the
// caller that can warn (NewEnv) does, exactly once per invocation.
//
// A RELATIVE $SCRUFF_STATE is refused rather
// than honoured, and that refusal is load-bearing: this state is
// machine-global, so resolving it against the
// process cwd scatters it into whatever directory scruff happened to be run
// from — routinely a git checkout, where it shows up as an untracked dir and
// can be swept into a `wip:` commit by scruff's own park path. An operator who
// wants state somewhere else can always say where absolutely; nobody has ever
// meant "put the machine's lease and ledger under my cwd".
func resolveStateDir() (dir, warning string) {
	if s := os.Getenv("SCRUFF_STATE"); s != "" {
		if filepath.IsAbs(s) {
			return s, ""
		}
		warning = fmt.Sprintf(
			"SCRUFF_STATE=%q is not an absolute path — ignoring it and using %s. "+
				"State is machine-global; a relative path would write it under the current directory.",
			s, defaultStateDir())
	}
	return defaultStateDir(), warning
}

// defaultStateDir is the scruff-named state directory, full stop. The
// holt-named fallback ended at 1.1.0 (docs/rename.md §8.1); leases are
// 90-second ephemera and the reap ledger restarts empty rather than carry the
// old path's spelling forever.
func defaultStateDir() string {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "scruff")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "state", "scruff")
}

// leasesAreSole reports whether a lease may answer for ABSENCE as well as
// presence — see occupancy.Leases.
//
// SCRUFF_OCCUPANCY=lease is the embedder's
// switch: it declares that every session in this deployment is one this tool
// spawned, so a lane nobody leased is a lane
// nobody is in. On a developer machine that is false — someone can always cd
// into a checkout without telling scruff — which is why the default is the
// cautious one, and why this is opt-in by an explicit env var rather than
// inferred from, say, the absence of lsof.
//
// It is an env var and not a config key on purpose: it describes the
// DEPLOYMENT, not the operator's taste, and the deployment is the thing that
// varies per process rather than per machine. The occupancy question itself
// wants to become a config seam (`occupied`, alongside HookLanded and
// HookPreserve) — that is the shape SPEC.md §14.2's callback lands in, and it
// is one more occupancy.Provider when it does.
func leasesAreSole() bool { return os.Getenv("SCRUFF_OCCUPANCY") == "lease" }

// defaultAgent is the client a new lane opens in when nothing says
// otherwise.
//
// The ladder, most explicit first: SCRUFF_AGENT is a
// one-invocation override; the `agent` hook is a program, for a machine that
// picks per repo or per time of
// day; the `agent` config key is the static answer, which is what almost
// everyone wants and costs no process; HAUS_AGENT_DEFAULT is a cutover
// fallback for pre-config rice builds; claude is the last word.
//
// A value that names a client scruff has never heard of is ignored at every rung
// rather than fatal — an unknown agent must not turn every `scruff new` into a
// hard failure when a working default is one rung down.
func (e *Env) defaultAgent() string {
	if a := os.Getenv("SCRUFF_AGENT"); registry.KnownAgent(a) {
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
	state, stateWarning := resolveStateDir()
	e := &Env{
		Base:      base,
		Reg:       reg,
		Cfg:       cfg,
		Cwd:       cwd,
		LeaseDir:  filepath.Join(state, "live"),
		LeaseSole: leasesAreSole(),
	}
	if stateWarning != "" {
		e.Warn(stateWarning)
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
// running `scruff reap` never saw "no forge CLI on PATH — nothing will be reaped
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
