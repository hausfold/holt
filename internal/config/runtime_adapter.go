package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RuntimeAdapter is a `kind = "runtime"` adapter (SPEC.md §5.5): the argv
// templates for standing up, entering, and tearing down an on-demand
// isolation backend — a VM, a container — that a lane can be handed into and
// pulled out of, explicitly. The default backend is "none": this loader is
// only ever reached once a caller has named one.
type RuntimeAdapter struct {
	ID       string
	Setup    []string // argv, unrendered — RenderArgv fills in the template
	Enter    []string
	Teardown []string
}

// LoadRuntimeAdapter reads ~/.config/holt/adapters/runtime/<id>.toml.
//
// A missing file is an error, not a silent Defer the way the policy-seam
// hooks in config.go behave: those hooks have a built-in to fall back to, but
// there is no built-in runtime backend, and `holt runtime up/enter/down` are
// explicit verbs a caller only reaches after naming a --backend — so a typo'd
// or unwritten one deserves a straight error naming the path it looked for,
// not a quiet no-op.
func LoadRuntimeAdapter(id string) (*RuntimeAdapter, error) {
	dir := Dir()
	if dir == "" {
		return nil, fmt.Errorf("no home directory to resolve a runtime adapter under")
	}
	path := filepath.Join(dir, "adapters", "runtime", id+".toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no runtime adapter %q — write one at %s (SPEC.md §5.5)", id, path)
	}

	adapter := &RuntimeAdapter{ID: id}
	for i, line := range strings.Split(string(raw), "\n") {
		text := strings.TrimSpace(stripComment(line))
		if text == "" {
			continue
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d isn't `key = value`: %q", path, i+1, text)
		}
		key = strings.TrimSpace(key)
		argv, ok := parseValue(strings.TrimSpace(val))
		if !ok {
			return nil, fmt.Errorf("%s:%d — couldn't read a string or a list of strings from %q", path, i+1, strings.TrimSpace(val))
		}
		switch key {
		case "setup":
			adapter.Setup = argv
		case "enter":
			adapter.Enter = argv
		case "teardown":
			adapter.Teardown = argv
		}
		// kind, id, and anything else this version doesn't know about are
		// ignored rather than fatal — the file describes one adapter, and a
		// key this loader doesn't recognize is forward compatibility, not a
		// typo.
	}
	return adapter, nil
}
