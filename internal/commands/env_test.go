package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAgent(t *testing.T) {
	t.Setenv("HOLT_AGENT", "")
	t.Setenv("NEBELHAUS_AGENT_DEFAULT", "claude")

	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if err := os.Mkdir(filepath.Join(config, "holt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "holt", "config.toml"), []byte("agent = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := defaultAgent(); got != "codex" {
		t.Fatalf("defaultAgent() = %q, want config agent codex", got)
	}

	t.Setenv("HOLT_AGENT", "opencode")
	if got := defaultAgent(); got != "opencode" {
		t.Fatalf("defaultAgent() = %q, want explicit HOLT_AGENT override", got)
	}
}
