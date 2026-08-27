package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuiltinRuntime is the one backend id scruff answers for with no config file.
const BuiltinRuntime = "tart"

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
	// Builtin names a backend scruff implements itself rather than reading argv
	// for. Empty for every adapter that came from a file, which is all of them
	// except `tart` — see internal/commands/runtime_tart.go for why that one
	// is different and why the file still wins when it exists.
	Builtin string
}

// LoadRuntimeAdapter reads ~/.config/holt/adapters/runtime/<id>.toml.
//
// A missing file is an error for every id but one, rather than a silent Defer
// the way the policy-seam hooks in config.go behave: `scruff runtime
// up/enter/down` are explicit verbs a caller only reaches after naming a
// --backend, so a typo'd or unwritten one deserves a straight error naming the
// path it looked for, not a quiet no-op.
//
// The exception is `tart`, which scruff implements itself — the setup step is a
// multi-command dance the three argv slots cannot hold, so leaving it to the
// file meant every standalone user writing the same script before they could
// use the verb at all. A tart.toml on disk still wins: the built-in is a
// default, not a reservation.
func LoadRuntimeAdapter(id string) (*RuntimeAdapter, error) {
	dir := Dir()
	if dir == "" {
		return nil, fmt.Errorf("no home directory to resolve a runtime adapter under")
	}
	path := filepath.Join(dir, "adapters", "runtime", id+".toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if id == BuiltinRuntime {
			return &RuntimeAdapter{ID: id, Builtin: id}, nil
		}
		return nil, fmt.Errorf("no runtime adapter %q — write one at %s (SPEC.md §5.5), or use the built-in %q backend", id, path, BuiltinRuntime)
	}

	keys, err := parseAdapterFile(path, raw)
	if err != nil {
		return nil, err
	}
	return &RuntimeAdapter{
		ID:       id,
		Setup:    keys["setup"],
		Enter:    keys["enter"],
		Teardown: keys["teardown"],
	}, nil
}
