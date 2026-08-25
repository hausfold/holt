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
	name, lane := e.laneFor(payload)
	args := []string{"send",
		"--kind", kind,
		"--title", name,
		"--body", body,
		"--source", "claude",
		"--thread", name,
		"--symbol", symbol,
	}
	// A banner that names a lane and can't take you to it is the whole
	// friction this hook was supposed to remove: you still had to find the
	// window yourself. `lane:` is trill's focus_lane action — it runs
	// `holt focus <name>` and nothing else — and it is qualified by repo
	// because two repos may hold the same lane name, which is exactly the
	// ambiguity `holt focus` refuses to guess at.
	//
	// Only when a registry row matched: a pane outside every lane still gets
	// a banner (named after its directory), and there is nothing to focus.
	if lane != "" {
		args = append(args, "--action", "Go to lane=lane:"+lane)
	}
	return args, true
}

// laneFor names the lane a hook payload came from: the registry row whose
// checkout contains the payload's cwd. A pane outside any lane (the main
// checkout, a ~ pane) still gets a banner — named after its directory, which
// is the honest answer and still not conversation content.
//
// Two names come back. The first is what the banner says, short enough to read
// at a glance. The second is what `holt focus` is given — `<repo>/<name>`, the
// same qualified spelling matchLane accepts — and it is empty for anything
// that isn't a lane, which is how the caller knows not to offer a click.
func (e *Env) laneFor(payload map[string]any) (name, lane string) {
	cwd, _ := hookField(payload, "cwd")
	if cwd == "" {
		return "claude", ""
	}
	if rows, err := e.Reg.Load(); err == nil {
		for _, row := range rows {
			if cwd == row.Path || strings.HasPrefix(cwd, row.Path+string(filepath.Separator)) {
				return row.Name, filepath.Base(row.Main) + "/" + row.Name
			}
		}
	}
	return filepath.Base(cwd), ""
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
