package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hausfold/scruff/internal/config"
)

// writeConfig plants a machine config for the test and returns an Env holding
// it, which is the only thing these tests need from a real environment.
func envWith(t *testing.T, body string) *Env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "scruff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "scruff", "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := config.Load()
	return &Env{Cfg: cfg, Cwd: t.TempDir()}
}

func TestDefaultAgentPrefersConfigOverLegacyEnv(t *testing.T) {
	t.Setenv("SCRUFF_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "claude")

	e := envWith(t, "agent = \"codex\"\n")
	if got := e.defaultAgent(); got != "codex" {
		t.Fatalf("defaultAgent() = %q, want the config's codex", got)
	}

	t.Setenv("SCRUFF_AGENT", "opencode")
	if got := e.defaultAgent(); got != "opencode" {
		t.Fatalf("defaultAgent() = %q, want the explicit SCRUFF_AGENT override", got)
	}
}

// The `agent` hook beats the static key, because a machine that runs a program
// to pick has more to say than one that wrote a constant.
func TestDefaultAgentHookBeatsConfigKey(t *testing.T) {
	t.Setenv("SCRUFF_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "")

	hook := writeHook(t, "agent-hook", `#!/bin/sh
echo '{"agent": "codex"}'
exit 0
`)
	e := envWith(t, "agent = \"claude\"\n\n[hooks]\nagent = \""+hook+"\"\n")
	if got := e.defaultAgent(); got != "codex" {
		t.Fatalf("defaultAgent() = %q, want the hook's codex", got)
	}
}

// A hook that defers leaves every rung below it exactly as it was — this is the
// property that makes an override safe to add to a working machine.
func TestDefaultAgentHookDeferFallsThrough(t *testing.T) {
	t.Setenv("SCRUFF_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "")

	hook := writeHook(t, "defer-hook", "#!/bin/sh\nexit 3\n")
	e := envWith(t, "agent = \"opencode\"\n\n[hooks]\nagent = \""+hook+"\"\n")
	if got := e.defaultAgent(); got != "opencode" {
		t.Fatalf("defaultAgent() = %q, want the config key the hook deferred to", got)
	}
}

// A hook that cannot run is a warning and a fallback, never a failure: scruff is
// in the path of every pane open, and a stale store path must not close that door.
func TestDefaultAgentBrokenHookWarnsAndFallsBack(t *testing.T) {
	t.Setenv("SCRUFF_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "")

	e := envWith(t, "agent = \"codex\"\n\n[hooks]\nagent = \"/nonexistent/scruff-agent-hook\"\n")
	if got := e.defaultAgent(); got != "codex" {
		t.Fatalf("defaultAgent() = %q, want the config key after the hook failed to run", got)
	}
	if len(e.Warnings) == 0 {
		t.Fatal("a hook that wouldn't run must produce a warning — a silently-dropped override is worse than none")
	}
}

func writeHook(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A relative SCRUFF_STATE is refused, not honoured: this state is machine-global,
// so resolving it against the cwd scatters leases and the reap ledger into
// whatever directory scruff was run from — which is how `scruff reap` in an agent
// pane created an untracked `live/` inside a git checkout.
func TestStateDirRefusesRelativeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	t.Setenv("SCRUFF_STATE", "live")

	dir, warning := resolveStateDir()
	if want := filepath.Join(home, "scruff"); dir != want {
		t.Errorf("state dir = %q, want the default %q", dir, want)
	}
	if warning == "" {
		t.Error("a silently ignored SCRUFF_STATE is worse than an honoured one — want a warning")
	}
}

// The guard has to hold where it is actually consumed, not only in the helper:
// LeaseDir and the ledger are the two things a relative override scattered.
func TestNewEnvKeepsStateOutOfTheCwdAndSaysSo(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CLAUDE_WT_BASE", t.TempDir())
	t.Setenv("SCRUFF_STATE", "live")

	e, err := NewEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(state, "scruff")
	if got := e.LeaseDir; got != filepath.Join(want, "live") {
		t.Errorf("LeaseDir = %q, want %q", got, filepath.Join(want, "live"))
	}
	if got := e.ledgerFile(); got != filepath.Join(want, "reaped.log") {
		t.Errorf("ledger = %q, want %q", got, filepath.Join(want, "reaped.log"))
	}
	if len(e.Warnings) != 1 {
		t.Errorf("warnings = %v, want exactly one — a silently ignored override is the worse failure", e.Warnings)
	}
}

func TestStateDirHonoursAbsoluteOverride(t *testing.T) {
	abs := t.TempDir()
	t.Setenv("SCRUFF_STATE", abs)

	dir, warning := resolveStateDir()
	if dir != abs {
		t.Errorf("state dir = %q, want %q", dir, abs)
	}
	if warning != "" {
		t.Errorf("unexpected warning for an absolute override: %q", warning)
	}
}

// ── the base (docs/rename.md §8.2) ───────────────────────────────────────────

// The env ladder at 1.1.0: SCRUFF_BASE, then CLAUDE_WT_BASE. The old
// spelling's rung is gone — setting it must do nothing, or the compat half
// would outlive its own deletion.
func TestBaseEnvLadder(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the default candidates resolve from here
	t.Setenv("SCRUFF_BASE", "")
	t.Setenv("CLAUDE_WT_BASE", "")
	t.Setenv("HOLT_BASE", t.TempDir()) // must be inert

	newBase, _ := defaultBaseCandidates()

	if got := baseDir(); got != newBase {
		t.Errorf("no env: base = %q, want the scruff default %q", got, newBase)
	}

	t.Setenv("SCRUFF_BASE", "/scruff-set")
	if got := baseDir(); got != "/scruff-set" {
		t.Errorf("SCRUFF_BASE set: base = %q, want /scruff-set", got)
	}

	t.Setenv("CLAUDE_WT_BASE", "/wt-set")
	if got := baseDir(); got != "/scruff-set" {
		t.Errorf("both set: base = %q, want /scruff-set — the plan-of-record ladder puts SCRUFF_BASE first", got)
	}

	t.Setenv("SCRUFF_BASE", "")
	if got := baseDir(); got != "/wt-set" {
		t.Errorf("CLAUDE_WT_BASE alone: base = %q, want /wt-set — SPEC.md §10's rung survives", got)
	}
}

// The default prefers the scruff-named base when it HOLDS a registry; the
// legacy path keeps serving only while it is the one holding the registry —
// a bare directory is not a base, and a machine that never had one starts
// under the name it keeps.
func TestBaseDefaultFallsBackToLegacyRegistryOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCRUFF_BASE", "")
	t.Setenv("CLAUDE_WT_BASE", "")
	t.Setenv("XDG_CACHE_HOME", "") // not read today; pinned so it never silently starts

	scruffBase := filepath.Join(home, ".cache", "scruff")
	legacyBase := filepath.Join(home, ".cache", "claude-worktrees")

	if got := baseDir(); got != scruffBase {
		t.Errorf("fresh machine: base = %q, want %q", got, scruffBase)
	}
	// A bare legacy directory (no registry) is NOT a base: the bash
	// predecessor always wrote one, so a registry-less dir is not ours.
	if err := os.MkdirAll(legacyBase, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := baseDir(); got != scruffBase {
		t.Errorf("legacy dir without a registry: base = %q, want %q — a bare directory is not a base", got, scruffBase)
	}
	if err := os.WriteFile(filepath.Join(legacyBase, "registry.tsv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := baseDir(); got != legacyBase {
		t.Errorf("legacy registry present: base = %q, want %q — skipping the migration must not break anyone", got, legacyBase)
	}
	// The scruff-named registry wins the moment it exists: post-migration, or
	// a fresh machine that wrote its own.
	if err := os.MkdirAll(scruffBase, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scruffBase, "registry.tsv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := baseDir(); got != scruffBase {
		t.Errorf("both registries: base = %q, want %q — the new base decides", got, scruffBase)
	}
}
