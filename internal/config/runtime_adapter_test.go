package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeRuntimeAdapter(t *testing.T, id, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	adapters := filepath.Join(dir, "holt", "adapters", "runtime")
	if err := os.MkdirAll(adapters, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapters, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRuntimeAdapterMissingFileIsAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := LoadRuntimeAdapter("apple-container")
	if err == nil {
		t.Fatal("a missing adapter file must be an error, not a silent default — the policy-seam hooks have a built-in to fall back to, a runtime backend that isn't tart does not")
	}
	if !strings.Contains(err.Error(), "apple-container") || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("error should name the adapter id and where it looked, got %q", err.Error())
	}
}

// tart is the one id that answers with no file: the setup step is a
// multi-command dance the argv slots can't hold, so leaving it to a file meant
// every standalone user writing the same script before the verb worked at all.
func TestLoadRuntimeAdapterTartIsBuiltIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	adapter, err := LoadRuntimeAdapter(BuiltinRuntime)
	if err != nil {
		t.Fatalf("tart must resolve with no adapter file: %v", err)
	}
	if adapter.Builtin != BuiltinRuntime {
		t.Errorf("Builtin = %q, want %q", adapter.Builtin, BuiltinRuntime)
	}
	if adapter.Setup != nil || adapter.Enter != nil || adapter.Teardown != nil {
		t.Error("a built-in carries no argv — the commands package dispatches on Builtin instead")
	}
}

// ...and a file with that id still wins, which is how haus (and anyone who
// wants a different image, guest user or share) overrides it.
func TestLoadRuntimeAdapterFileBeatsBuiltIn(t *testing.T) {
	writeRuntimeAdapter(t, BuiltinRuntime, "kind = \"runtime\"\nid = \"tart\"\nsetup = [\"mine\", \"setup\"]\n")
	adapter, err := LoadRuntimeAdapter(BuiltinRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Builtin != "" {
		t.Error("a tart.toml on disk must load as an ordinary file-backed adapter, not the built-in")
	}
	if !reflect.DeepEqual(adapter.Setup, []string{"mine", "setup"}) {
		t.Errorf("Setup = %v, want the file's own argv", adapter.Setup)
	}
}

func TestLoadRuntimeAdapterMalformedTOML(t *testing.T) {
	writeRuntimeAdapter(t, "broken", "kind = \"runtime\"\nthis line has no equals sign\n")
	if _, err := LoadRuntimeAdapter("broken"); err == nil {
		t.Fatal("a line that isn't `key = value` must be an error")
	}
}

func TestLoadRuntimeAdapterValidThreeKeyFile(t *testing.T) {
	writeRuntimeAdapter(t, "tart", `
kind = "runtime"
id   = "tart"
setup = ["tart", "clone", "base", "holt-{{.Name}}"]
enter = ["ssh", "admin@holt-{{.Name}}"]
teardown = ["tart", "delete", "holt-{{.Name}}", "--force"]
`)
	adapter, err := LoadRuntimeAdapter("tart")
	if err != nil {
		t.Fatalf("valid adapter file failed to load: %v", err)
	}
	if adapter.ID != "tart" {
		t.Fatalf("ID = %q, want tart", adapter.ID)
	}
	if want := []string{"tart", "clone", "base", "holt-{{.Name}}"}; !reflect.DeepEqual(adapter.Setup, want) {
		t.Fatalf("Setup = %q, want %q", adapter.Setup, want)
	}
	if want := []string{"ssh", "admin@holt-{{.Name}}"}; !reflect.DeepEqual(adapter.Enter, want) {
		t.Fatalf("Enter = %q, want %q", adapter.Enter, want)
	}
	if want := []string{"tart", "delete", "holt-{{.Name}}", "--force"}; !reflect.DeepEqual(adapter.Teardown, want) {
		t.Fatalf("Teardown = %q, want %q", adapter.Teardown, want)
	}
}

func TestLoadRuntimeAdapterIgnoresUnknownKeys(t *testing.T) {
	writeRuntimeAdapter(t, "minimal", `kind = "runtime"
id = "minimal"
setup = ["echo", "up"]
`)
	adapter, err := LoadRuntimeAdapter("minimal")
	if err != nil {
		t.Fatalf("kind/id must be ignored, not fatal: %v", err)
	}
	if want := []string{"echo", "up"}; !reflect.DeepEqual(adapter.Setup, want) {
		t.Fatalf("Setup = %q, want %q", adapter.Setup, want)
	}
	if len(adapter.Enter) != 0 || len(adapter.Teardown) != 0 {
		t.Fatalf("an adapter that names only setup must leave enter/teardown empty, got %+v", adapter)
	}
}

func TestRenderArgvSubstitutesVariables(t *testing.T) {
	vars := TemplateVars{Name: "sparkle", Path: "/lane/sparkle", Repo: "hausfold/holt"}
	got, err := RenderArgv([]string{"tart", "clone", "base", "holt-{{.Name}}", "--path", "{{.Path}}"}, vars)
	if err != nil {
		t.Fatalf("RenderArgv failed: %v", err)
	}
	want := []string{"tart", "clone", "base", "holt-sparkle", "--path", "/lane/sparkle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderArgv = %q, want %q", got, want)
	}
}

// A lane or branch name containing shell metacharacters must stay ONE argv
// element after rendering — RenderArgv never re-parses its own output, so
// there is nothing for a `;` or `$(...)` to break out of.
func TestRenderArgvNoShellMetacharInjection(t *testing.T) {
	vars := TemplateVars{Name: "sparkle; rm -rf ~ $(whoami)"}
	got, err := RenderArgv([]string{"echo", "{{.Name}}"}, vars)
	if err != nil {
		t.Fatalf("RenderArgv failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("a dangerous value must still render as ONE argv element, got %d: %q", len(got), got)
	}
	if got[1] != "sparkle; rm -rf ~ $(whoami)" {
		t.Fatalf("got[1] = %q, want the literal value untouched", got[1])
	}
}

func TestRenderArgvEmptyIsEmpty(t *testing.T) {
	got, err := RenderArgv(nil, TemplateVars{})
	if err != nil || got != nil {
		t.Fatalf("RenderArgv(nil, ...) = %q, %v — want nil, nil", got, err)
	}
}
