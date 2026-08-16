package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hausfold/holt/internal/config"
)

// writeConfig plants a machine config for the test and returns an Env holding
// it, which is the only thing these tests need from a real environment.
func envWith(t *testing.T, body string) *Env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "holt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "holt", "config.toml"), []byte(body), 0o644); err != nil {
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

// A hook that cannot run is a warning and a fallback, never a failure: holt is
// in the path of every pane open, and a stale store path must not close that door.
func TestDefaultAgentBrokenHookWarnsAndFallsBack(t *testing.T) {
	t.Setenv("HOLT_AGENT", "")
	t.Setenv("HAUS_AGENT_DEFAULT", "")

	e := envWith(t, "agent = \"codex\"\n\n[hooks]\nagent = \"/nonexistent/holt-agent-hook\"\n")
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
