package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeNamerAdapter(t *testing.T, id, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	adapters := filepath.Join(dir, "scruff", "adapters", "namer")
	if err := os.MkdirAll(adapters, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapters, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claude is the one id that answers with no file, for the same reason tart is:
// it is the client a machine spawning agents already has, so the common case
// costs one config line instead of a file nobody would write differently.
func TestLoadNamerAdapterClaudeIsBuiltIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	adapter, err := LoadNamerAdapter(BuiltinNamer)
	if err != nil {
		t.Fatalf("claude must resolve with no adapter file: %v", err)
	}
	if adapter.Name[0] != "claude" {
		t.Errorf("Name = %q, want the claude client", adapter.Name)
	}
	// The prompt is DATA: a brief routinely starts with a dash, and a bare
	// positional would be read as a flag. Same rule as every agent adapter.
	var sawSeparator bool
	for i, a := range adapter.Name {
		if a == "--" {
			sawSeparator = true
		}
		if a == "{{.Prompt}}" && !sawSeparator {
			t.Errorf("Name[%d] is the prompt with no `--` before it: %q", i, adapter.Name)
		}
	}
	if !sawSeparator {
		t.Errorf("Name = %q, want option parsing ended before the prompt", adapter.Name)
	}
}

// …and a file with that id still wins, which is how someone swaps the model,
// the client, or the whole approach.
func TestLoadNamerAdapterFileBeatsBuiltIn(t *testing.T) {
	writeNamerAdapter(t, BuiltinNamer, "kind = \"namer\"\nid = \"claude\"\nname = [\"mine\", \"{{.Prompt}}\"]\n")
	adapter, err := LoadNamerAdapter(BuiltinNamer)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"mine", "{{.Prompt}}"}; !reflect.DeepEqual(adapter.Name, want) {
		t.Errorf("Name = %v, want the file's own argv", adapter.Name)
	}
}

func TestLoadNamerAdapterMissingFileIsAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := LoadNamerAdapter("ollama")
	if err == nil {
		t.Fatal("a namer named in the config and missing on disk must say so — the caller downgrades it to a warning, the loader does not hide it")
	}
	if !strings.Contains(err.Error(), "ollama") || !strings.Contains(err.Error(), "namer") {
		t.Errorf("error should name the adapter id and where it looked, got %q", err.Error())
	}
}

// A namer adapter IS its one argv. A file without it would otherwise load
// happily and then produce a warning per lane with no clue where to look.
func TestLoadNamerAdapterWithoutANameCommand(t *testing.T) {
	writeNamerAdapter(t, "empty", "kind = \"namer\"\nid = \"empty\"\n")
	_, err := LoadNamerAdapter("empty")
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("an adapter with no `name` key must say so, got %v", err)
	}
}

func TestLoadNamerAdapterMalformedTOML(t *testing.T) {
	writeNamerAdapter(t, "broken", "kind = \"namer\"\nthis line has no equals sign\n")
	if _, err := LoadNamerAdapter("broken"); err == nil {
		t.Fatal("a line that isn't `key = value` must be an error")
	}
}

func TestBuiltinNamerTOMLParsesAsAFileWould(t *testing.T) {
	// SPEC.md §5.1: a built-in is exactly the file a user would write, and
	// this one is the file `docs/reference.md` tells them to copy.
	keys, err := parseAdapterFile("builtin", []byte(BuiltinNamerTOML))
	if err != nil {
		t.Fatalf("the built-in must parse through the ordinary loader: %v", err)
	}
	if got := keys["kind"]; len(got) != 1 || got[0] != "namer" {
		t.Errorf("kind = %v, want namer", got)
	}
	if got := keys["id"]; len(got) != 1 || got[0] != BuiltinNamer {
		t.Errorf("id = %v, want %q", got, BuiltinNamer)
	}
}

func TestNamerConfigKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "scruff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scruff", "config.toml"),
		[]byte("agent = \"codex\"\nnamer = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := Load()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.Namer != "claude" || cfg.Agent != "codex" {
		t.Fatalf("Namer = %q, Agent = %q — want claude, codex", cfg.Namer, cfg.Agent)
	}
}

// No key is the default, and the default is the behaviour scruff had before the
// key existed: nothing runs, and an unnamed lane keeps its random word pair.
func TestNamerIsUnsetByDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, _ := Load()
	if cfg.Namer != "" {
		t.Fatalf("Namer = %q with no config file, want empty", cfg.Namer)
	}
}
