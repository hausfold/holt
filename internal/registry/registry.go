// Package registry is holt's source of truth.
//
// Not the filesystem, and not `git worktree list` — both are derived and both
// lie. A parked worktree has no checkout on disk at all (the branch IS the
// work), and a half-finished `git worktree remove` leaves a directory git has
// already disowned. Only the registry knows the whole set.
//
// # Format
//
// 0.1 reads and writes the EXISTING bash-`wt` TSV byte-compatibly (SPEC.md
// §2.1). This is the hard requirement of the cutover: Julien's machine has live
// rows written by the shell version, and cutover day must not migrate anything.
// One tab-separated line per worktree, keyed on field 4 (the checkout path):
//
//	name <TAB> main <TAB> branch <TAB> path <TAB> parent <TAB> agent
//
// A row with fewer than six fields predates the client column and means
// "claude" — the v0 migration case, which must keep working forever.
package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Row is one worktree's registry entry.
type Row struct {
	Name   string // worktree name — the branch minus the "worktree-" prefix
	Main   string // the repo's main checkout
	Branch string // full branch name
	Path   string // checkout path — the primary key
	Parent string // cwd of the pane that spawned it, or "" — see cmd child
	Agent  string // client id: claude | codex | opencode
}

// Registry is a handle on one registry file.
type Registry struct {
	path string
}

// DefaultAgent is used when a row names no client and none is supplied. It is
// overridden by the caller (which knows about NEBELHAUS_AGENT_DEFAULT and the
// adapter set); this is only the floor.
var DefaultAgent = "claude"

// KnownAgent reports whether an id names a client holt can drive. It is a
// variable so the adapter layer can replace it in 0.2 without this package
// growing a dependency on it.
var KnownAgent = func(id string) bool {
	switch id {
	case "claude", "codex", "opencode":
		return true
	}
	return false
}

// Open returns a handle on the registry at path, creating its directory.
func Open(path string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Registry{path: path}, nil
}

// Path is the registry file's location.
func (r *Registry) Path() string { return r.path }

// Load returns every row. A missing file is an empty registry, not an error —
// the first `holt` on a machine must work.
func (r *Registry) Load() ([]Row, error) {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(string(b)), nil
}

func parse(s string) []Row {
	var rows []Row
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		// Fewer than four fields is not a row we can key on. Skip rather than
		// guess — a corrupt line must not poison the whole listing.
		if len(f) < 4 {
			continue
		}
		row := Row{Name: f[0], Main: f[1], Branch: f[2], Path: f[3]}
		if len(f) > 4 {
			row.Parent = f[4]
		}
		if len(f) > 5 {
			row.Agent = f[5]
		}
		// The v0 case: a row written before the client column existed is a
		// Claude worktree, and must not reopen in whatever the default is now.
		if !KnownAgent(row.Agent) {
			row.Agent = "claude"
		}
		rows = append(rows, row)
	}
	return rows
}

func format(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join([]string{r.Name, r.Main, r.Branch, r.Path, r.Parent, r.Agent}, "\t"))
		b.WriteByte('\n')
	}
	return b.String()
}

// Find returns the row for a checkout path.
func (r *Registry) Find(path string) (Row, bool) {
	rows, _ := r.Load()
	for _, row := range rows {
		if row.Path == path {
			return row, true
		}
	}
	return Row{}, false
}

// Put upserts a row, keyed on Path.
//
// Empty Parent or Agent PRESERVE whatever the existing row had rather than
// blanking it: resume knows the path but not the original spawner, and losing
// the parent there would orphan the row in the statusline's child listing.
func (r *Registry) Put(row Row) error {
	return r.mutate(func(rows []Row) []Row {
		var out []Row
		for _, existing := range rows {
			if existing.Path == row.Path {
				if row.Parent == "" {
					row.Parent = existing.Parent
				}
				if row.Agent == "" {
					row.Agent = existing.Agent
				}
				continue // drop; the new row is appended below
			}
			out = append(out, existing)
		}
		if !KnownAgent(row.Agent) {
			row.Agent = DefaultAgent
		}
		return append(out, row)
	})
}

// Delete drops the row for a checkout path.
func (r *Registry) Delete(path string) error {
	return r.mutate(func(rows []Row) []Row {
		out := rows[:0]
		for _, row := range rows {
			if row.Path != path {
				out = append(out, row)
			}
		}
		return out
	})
}

// Prune keeps only the rows for which keep returns true.
func (r *Registry) Prune(keep func(Row) bool) error {
	return r.mutate(func(rows []Row) []Row {
		out := rows[:0]
		for _, row := range rows {
			if keep(row) {
				out = append(out, row)
			}
		}
		return out
	})
}

// mutate applies fn to the registry under an exclusive lock.
//
// The bash version rewrote the whole table through a temp file with an advisory
// mkdir lock, which is a genuine lost-update race when two panes close at once.
// It has not bitten yet, and that is luck rather than design. Here the read,
// the transform and the write all happen inside one flock.
func (r *Registry) mutate(fn func([]Row) []Row) error {
	unlock, err := lock(r.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	b, err := os.ReadFile(r.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := format(fn(parse(string(b))))

	// Temp file + rename, so a crash mid-write can never leave a half-registry.
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".registry-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(next); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}
