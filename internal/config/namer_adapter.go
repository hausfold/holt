package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuiltinNamer is the one namer id holt answers for with no adapter file.
const BuiltinNamer = "claude"

// BuiltinNamerTOML is that built-in, spelled as the file that would replace it.
//
// It is parsed by the same loader as a file somebody wrote, because SPEC.md
// §5.1 says a built-in is exactly the file a user would write — there is no
// privileged code path here, and `claude.toml` on disk shadows this wholesale.
//
// Every flag in it earns its place: `-p` is the one-shot, non-interactive mode;
// `--model haiku` is the cheapest and fastest client-side model, an alias
// rather than a pinned id so it follows the model line rather than rotting;
// `--strict-mcp-config` drops the machine's MCP servers, which the naming turn
// cannot use and which measured at roughly half this call's wall clock; and the
// `--` is the rule SPEC.md §5.3 states for every prompt-carrying argv — a brief
// routinely begins with a dash, and a bare positional would be read as a flag.
const BuiltinNamerTOML = `kind = "namer"
id   = "claude"
name = ["claude", "-p", "--model", "haiku", "--strict-mcp-config", "--", "{{.Prompt}}"]
`

// NamerAdapter is a `kind = "namer"` adapter (SPEC.md §5.6): one argv that
// turns a lane's first-turn task into a name for the lane.
//
// The command is handed holt's whole naming REQUEST as `{{.Prompt}}` — the
// instruction, the repo, the names already taken and the task itself, composed
// by holt — and answers on stdout with the name and nothing else. holt owns the
// wording so that name quality is holt's problem rather than every adapter
// file's; an adapter that wants its own wording wraps a script here and reshapes
// the text it was given.
type NamerAdapter struct {
	ID   string
	Name []string // argv, unrendered — RenderArgv fills in the template
}

// LoadNamerAdapter reads ~/.config/holt/adapters/namer/<id>.toml.
//
// A missing file is an error for every id but `claude`, the same way runtime
// backends behave and for the same reason: a namer is named explicitly in the
// config, so a typo'd one has to say so rather than quietly leaving lanes
// unnamed. What the CALLER does with that error is the softer half — naming is
// cosmetic, so `holt new` reports it and falls back to a random name rather
// than refusing to make the lane.
func LoadNamerAdapter(id string) (*NamerAdapter, error) {
	dir := Dir()
	if dir == "" {
		return nil, fmt.Errorf("no home directory to resolve a namer adapter under")
	}
	path := filepath.Join(dir, "adapters", "namer", id+".toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if id != BuiltinNamer {
			return nil, fmt.Errorf("no namer adapter %q — write one at %s (SPEC.md §5.6), or use the built-in %q", id, path, BuiltinNamer)
		}
		raw, path = []byte(BuiltinNamerTOML), "the built-in "+BuiltinNamer+" namer"
	}
	keys, err := parseAdapterFile(path, raw)
	if err != nil {
		return nil, err
	}
	if len(keys["name"]) == 0 {
		return nil, fmt.Errorf("%s declares no `name` command — a namer adapter is that one argv", path)
	}
	return &NamerAdapter{ID: id, Name: keys["name"]}, nil
}
