package config

import (
	"fmt"
	"strings"
)

// parseAdapterFile reads an adapter TOML into its keys.
//
// One parser for every adapter kind (SPEC.md §5), because the file format is
// the same one in each: a flat table of `key = "string"` or
// `key = ["an", "argv", "list"]`, no sections, no nesting. Each loader picks
// the keys it knows out of the map.
//
// A key a loader doesn't recognize — `kind`, `id`, anything a later version
// adds — comes back in the map and is ignored there rather than being fatal:
// an unknown key is forward compatibility, not a typo. A line that isn't
// `key = value` at all IS fatal, because an adapter is named explicitly and a
// caller who misspelled one deserves the line number rather than a command
// that silently does nothing.
func parseAdapterFile(path string, raw []byte) (map[string][]string, error) {
	keys := map[string][]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		text := strings.TrimSpace(stripComment(line))
		if text == "" {
			continue
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d isn't `key = value`: %q", path, i+1, text)
		}
		argv, ok := parseValue(strings.TrimSpace(val))
		if !ok {
			return nil, fmt.Errorf("%s:%d — couldn't read a string or a list of strings from %q", path, i+1, strings.TrimSpace(val))
		}
		keys[strings.TrimSpace(key)] = argv
	}
	return keys, nil
}
