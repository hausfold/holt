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
	t.Setenv("HOLT_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "claude")

	e := envWith(t, "agent = \"codex\"\n")
	if got := e.defaultAgent(); got != "codex" {
		t.Fatalf("defaultAgent() = %q, want the config's codex", got)
	}

	t.Setenv("HOLT_AGENT", "opencode")
	if got := e.defaultAgent(); got != "opencode" {
		t.Fatalf("defaultAgent() = %q, want the explicit HOLT_AGENT override", got)
	}
}

// The `agent` hook beats the static key, because a machine that runs a program
// to pick has more to say than one that wrote a constant.
func TestDefaultAgentHookBeatsConfigKey(t *testing.T) {
	t.Setenv("HOLT_AGENT", "")
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
	t.Setenv("HOLT_AGENT", "")
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
	t.Setenv("HOLT_AGENT", "")
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
	t.Setenv("HOLT_STATE", "live")

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
	t.Setenv("HOLT_STATE", "live")

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
	t.Setenv("HOLT_STATE", abs)

	dir, warning := resolveStateDir()
	if dir != abs {
		t.Errorf("state dir = %q, want %q", dir, abs)
	}
	if warning != "" {
		t.Errorf("unexpected warning for an absolute override: %q", warning)
	}
}

// ── the rename (docs/rename.md §3) ───────────────────────────────────────────
//
// These die with internal/compat at 1.1.0. Until then they are the proof that
// ONE binary answers to both names, which is the only reason haus and this repo
// can move in either order.

// Both spellings resolve, and the new one wins when both are set — the updated
// half of the machine is the half that decides.
func TestEnvVarsAnswerToBothNames(t *testing.T) {
	t.Setenv("CLAUDE_WT_BASE", "")
	t.Setenv("HOLT_BASE", "/old")
	if got := baseDir(); got != "/old" {
		t.Errorf("HOLT_BASE alone: base = %q, want %q — an old haus must keep working", got, "/old")
	}
	t.Setenv("SCRUFF_BASE", "/new")
	if got := baseDir(); got != "/new" {
		t.Errorf("both set: base = %q, want %q — the new spelling decides", got, "/new")
	}

	t.Setenv("HOLT_AGENT", "codex")
	e := &Env{Cfg: &config.Config{}}
	if got := e.defaultAgent(); got != "codex" {
		t.Errorf("HOLT_AGENT alone: agent = %q, want codex", got)
	}
	t.Setenv("SCRUFF_AGENT", "opencode")
	if got := e.defaultAgent(); got != "opencode" {
		t.Errorf("both set: agent = %q, want opencode", got)
	}
}

// CLAUDE_WT_BASE keeps its priority over BOTH spellings. It predates them and
// answers to neither — SPEC.md §10's cutover rung, untouched by this rename.
func TestClaudeWTBaseStillOutranksBothSpellings(t *testing.T) {
	t.Setenv("CLAUDE_WT_BASE", "/wt")
	t.Setenv("SCRUFF_BASE", "/new")
	t.Setenv("HOLT_BASE", "/old")
	if got := baseDir(); got != "/wt" {
		t.Errorf("base = %q, want %q", got, "/wt")
	}
}

// A machine that already has ~/.local/state/holt keeps using it; a fresh one
// starts under the name it will keep. Neither case moves a file.
func TestStateDirFallsBackToTheOldDirOnlyWhenItExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	t.Setenv("SCRUFF_STATE", "")
	t.Setenv("HOLT_STATE", "")

	if got, want := stateDir(), filepath.Join(home, "scruff"); got != want {
		t.Errorf("fresh machine: state dir = %q, want %q", got, want)
	}
	if err := os.MkdirAll(filepath.Join(home, "holt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := stateDir(), filepath.Join(home, "holt"); got != want {
		t.Errorf("existing holt dir: state dir = %q, want %q — state must not appear to vanish", got, want)
	}
	if err := os.MkdirAll(filepath.Join(home, "scruff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := stateDir(), filepath.Join(home, "scruff"); got != want {
		t.Errorf("both dirs: state dir = %q, want %q", got, want)
	}
}
