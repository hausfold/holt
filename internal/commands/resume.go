package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/registry"
	"github.com/nebelhaus/holt/internal/ui"
)

// Resume rebuilds a worktree's checkout and reopens the agent chat that made it.
//
// `want` is a name, or `<repo>/<name>` when the same name exists in two repos.
func (e *Env) Resume(want string) error {
	if want == "" {
		return e.List(false)
	}
	repo, name := "", want
	if i := strings.Index(want, "/"); i >= 0 {
		repo, name = want[:i], want[i+1:]
	}

	var matches []Entry
	for _, entry := range e.discover() {
		if !e.branchAlive(entry) || entry.Name() != name {
			continue
		}
		if repo != "" && filepath.Base(entry.Main) != repo {
			continue
		}
		matches = append(matches, entry)
	}
	switch len(matches) {
	case 0:
		return exitcode.Usagef("no agent worktree named '%s' — run: holt", want)
	case 1:
	default:
		return exitcode.Usagef("'%s' exists in more than one repo — qualify it: holt <repo>/%s", name, name)
	}
	entry := matches[0]

	// Resolve the client BEFORE the checkout is re-registered: a five-column
	// registry row predates the client field and is Claude forever, even if the
	// machine's default has since changed.
	agent := e.agentForPath(entry.Path)

	switch entry.State {
	case Live:
		ui.Say("'%s' is still live at %s", entry.Branch, entry.Path)

	case Stray:
		// A husk: the directory is there, git disowns it. It is in the way —
		// `git worktree add` refuses a non-empty directory — and it may hold
		// real uncommitted work, so it is MOVED, never deleted. The rebuilt
		// checkout beside it has the branch's committed state; whatever was only
		// in the husk is one `diff -ru` away, and the path is printed so it
		// cannot be lost silently.
		husk := entry.Path + ".stray-" + time.Now().Format("20060102-150405")
		if err := os.Rename(entry.Path, husk); err != nil {
			return exitcode.Usagef("couldn't move the dangling checkout aside: %s", entry.Path)
		}
		ui.Say("dangling checkout moved to %s (nothing deleted — it may hold uncommitted work)", husk)
		if err := e.rebuild(entry, agent); err != nil {
			return err
		}
		ui.Say("compare what the husk had: diff -ru %s %s", husk, entry.Path)

	default: // Parked
		if err := e.rebuild(entry, agent); err != nil {
			return err
		}
	}

	// A spawned worktree (`holt child`, or a nested spawn) has no chat of its
	// own — reopen the session that spawned it. The checkout above is still
	// rebuilt either way, so the branch's files are on disk; this only decides
	// which directory the client's picker opens in.
	chat := e.chatHome(agent, entry.Path)
	if chat != entry.Path {
		ui.Say("no chat in this worktree — it was spawned from a session in %s", chat)
		// A shared parent checkout (a workshop pane that spawned several
		// children) lists many sessions in its picker — point at the right one.
		ui.Say("in the picker, pick the session for '%s' — last commit:", entry.Branch)
		ui.Say("  %s", gitx.Subject(entry.Main, entry.Branch))
		// Claude keys the transcript off the cwd, so that directory has to
		// exist. If the parent checkout was reaped, anchor a bare dir purely to
		// reopen the chat — the work is safe on the branch, and the child
		// checkout with the files was rebuilt above.
		_ = os.MkdirAll(chat, 0o755)
	}

	spec, known := specFor(agent)
	if !known {
		return exitcode.Usagef("unknown agent %q recorded for this worktree", agent)
	}
	if ui.IsTTY(os.Stdout) && clientInstalled(agent) {
		ui.Say("reopening the %s chat …", agent)
		if err := os.Chdir(chat); err != nil {
			return exitcode.Usagef("could not enter %s", chat)
		}
		return execClient(spec.resume)
	}
	// No tty (piped, or driven by a script) or no client installed: print the
	// command rather than exec into something nobody can see.
	ui.Say("checkout ready. Reopen the %s chat with:", agent)
	ui.Out("    cd %s && %s\n", shellQuote(chat), strings.Join(spec.resume, " "))
	return nil
}

// rebuild re-adds a checkout for a branch that still exists.
func (e *Env) rebuild(entry Entry, agent string) error {
	ui.Say("rebuilding checkout for %s → %s", entry.Branch, entry.Path)
	if err := os.MkdirAll(filepath.Dir(entry.Path), 0o755); err != nil {
		return err
	}
	if out, err := gitx.Run(entry.Main, "worktree", "add", entry.Path, entry.Branch); err != nil {
		return exitcode.Usagef("git worktree add failed: %v", err)
	} else if out != "" {
		ui.Say("%s", out)
	}
	// Empty Parent preserves whatever the existing row had — resume knows the
	// path but not the original spawner, and blanking it would orphan the row.
	_ = e.Reg.Put(registry.Row{
		Name: entry.Name(), Main: entry.Main, Branch: entry.Branch,
		Path: entry.Path, Agent: agent,
	})
	return nil
}

func clientInstalled(id string) bool {
	_, err := exec.LookPath(id)
	return err == nil
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()&;|<>#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
