package commands

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HookNotify implements the Notification/Stop hook: the client's "blocked on
// the user" / "finished the turn" events become a trill banner for the lane —
// an `ask` that trill's ledge parks until answered, or a `done`.
//
// Unlike its create/remove siblings this hook changes NOTHING — no checkout,
// no registry row — so it must also never be the reason a session breaks. It
// exits 0 whatever happens: a payload that isn't JSON, an event it doesn't
// recognise, no trill binary anywhere, the daemon down (trill exit 2). Every
// one of those means "no banner", never an error the client surfaces
// mid-session.
func (e *Env) HookNotify(stdin io.Reader) error {
	payload, err := readHookPayload(stdin)
	if err != nil {
		return nil
	}
	args, ok := e.trillSendArgs(payload)
	if !ok {
		return nil
	}
	bin := trillBinary()
	if bin == "" {
		return nil
	}
	// Bounded, and quiet: a wedged daemon must not hang pane redraw, and
	// trill's own stderr is not this hook's to surface.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	_ = cmd.Run()
	return nil
}

// trillSendArgs maps one hook payload to a `trill send` argv, or declines.
//
// The title and thread carry the lane name and nothing else — never the
// payload's message string, which is conversation content a banner has no
// business showing on a shared screen.
func (e *Env) trillSendArgs(payload map[string]any) ([]string, bool) {
	event, _ := hookField(payload, "hook_event_name")
	var kind, body, symbol string
	switch event {
	case "Notification":
		kind, body, symbol = "ask", "waiting on you", "questionmark.bubble"
	case "Stop":
		// A Stop with stop_hook_active is a stop hook steering the session
		// mid-loop, not a finished turn — a done banner per iteration is noise.
		if active, _ := payload["stop_hook_active"].(bool); active {
			return nil, false
		}
		kind, body, symbol = "done", "finished its turn", "checkmark.circle"
	default:
		return nil, false
	}
	name := e.laneNameFor(payload)
	return []string{"send",
		"--kind", kind,
		"--title", name,
		"--body", body,
		"--source", "claude",
		"--thread", name,
		"--symbol", symbol,
	}, true
}

// laneNameFor names the lane a hook payload came from: the registry row whose
// checkout contains the payload's cwd. A pane outside any lane (the main
// checkout, a ~ pane) still gets a banner — named after its directory, which
// is the honest answer and still not conversation content.
func (e *Env) laneNameFor(payload map[string]any) string {
	cwd, _ := hookField(payload, "cwd")
	if cwd == "" {
		return "claude"
	}
	if rows, err := e.Reg.Load(); err == nil {
		for _, row := range rows {
			if cwd == row.Path || strings.HasPrefix(cwd, row.Path+string(filepath.Separator)) {
				return row.Name
			}
		}
	}
	return filepath.Base(cwd)
}

// trillBinary resolves the trill CLI without assuming anything about the
// machine: Trill.app is routinely installed while `trill` is on nobody's PATH,
// because the app binary IS the CLI. HOLT_TRILL is authoritative when set —
// including set to something missing, which is how a machine (or a test) says
// "no banners" — and an empty answer means exactly that, silently.
func trillBinary() string {
	if p := os.Getenv("HOLT_TRILL"); p != "" {
		if isExecutable(p) {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("trill"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	for _, p := range []string{
		filepath.Join(home, "Applications", "Trill.app", "Contents", "MacOS", "Trill"),
		"/Applications/Trill.app/Contents/MacOS/Trill",
	} {
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
