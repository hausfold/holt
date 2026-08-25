package commands

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hausfold/holt/internal/registry"
)

// notifyEnv is an Env with a registry holding one lane, for the cwd → lane
// name resolution the notify hook does.
func notifyEnv(t *testing.T) (*Env, registry.Row) {
	t.Helper()
	dir := t.TempDir()
	reg, err := registry.Open(filepath.Join(dir, "registry.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	row := registry.Row{
		Name: "sparkle", Main: filepath.Join(dir, "repo"),
		Branch: "worktree-sparkle",
		Path:   filepath.Join(dir, "wtbase", "repo", "sparkle"),
		Agent:  "claude",
	}
	if err := reg.Put(row); err != nil {
		t.Fatal(err)
	}
	return &Env{Reg: reg}, row
}

func TestTrillSendArgsMapsNotificationToAsk(t *testing.T) {
	e, row := notifyEnv(t)
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Notification",
		"cwd":             row.Path,
		"message":         "Claude needs your permission to use Bash",
	})
	if !ok {
		t.Fatal("a Notification event must produce a send")
	}
	joined := strings.Join(args, " ")
	if !slices.Contains(args, "ask") {
		t.Fatalf("want --kind ask, got %q", joined)
	}
	if !slices.Contains(args, row.Name) {
		t.Fatalf("want the lane name %q in the argv, got %q", row.Name, joined)
	}
	// The payload's message is conversation content — it must never reach trill.
	if strings.Contains(joined, "permission") {
		t.Fatalf("the payload message leaked into the argv: %q", joined)
	}
}

func TestTrillSendArgsMapsStopToDone(t *testing.T) {
	e, row := notifyEnv(t)
	// cwd is a SUBDIRECTORY of the lane — the resolution is containment, not
	// equality, because a session cds around its checkout.
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Stop",
		"cwd":             filepath.Join(row.Path, "internal", "deep"),
	})
	if !ok {
		t.Fatal("a Stop event must produce a send")
	}
	if !slices.Contains(args, "done") || !slices.Contains(args, row.Name) {
		t.Fatalf("want --kind done for lane %q, got %q", row.Name, strings.Join(args, " "))
	}
}

// A Stop mid-stop-hook-loop is not a finished turn; one banner per iteration
// would be noise.
func TestTrillSendArgsSkipsActiveStopHook(t *testing.T) {
	e, row := notifyEnv(t)
	if _, ok := e.trillSendArgs(map[string]any{
		"hook_event_name":  "Stop",
		"cwd":              row.Path,
		"stop_hook_active": true,
	}); ok {
		t.Fatal("stop_hook_active must suppress the send")
	}
}

func TestTrillSendArgsDeclinesUnknownEvents(t *testing.T) {
	e, _ := notifyEnv(t)
	for _, event := range []string{"", "PreToolUse", "SessionEnd"} {
		if _, ok := e.trillSendArgs(map[string]any{"hook_event_name": event, "cwd": "/x"}); ok {
			t.Fatalf("event %q must not produce a send", event)
		}
	}
}

// A pane outside any lane still banners, named after its directory — and
// carries no click, because there is no lane for `holt focus` to go to.
func TestTrillSendArgsFallsBackToDirectoryName(t *testing.T) {
	e, _ := notifyEnv(t)
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Stop",
		"cwd":             "/somewhere/else/mytool",
	})
	if !ok || !slices.Contains(args, "mytool") {
		t.Fatalf("want the cwd basename as the title, got %q", strings.Join(args, " "))
	}
	if slices.Contains(args, "--action") {
		t.Fatalf("a pane that is not a lane must offer no lane action, got %q", strings.Join(args, " "))
	}
}

// The banner is clickable, and the lane it names is qualified by repo — the
// same spelling `holt focus` (and matchLane behind it) accepts, because one
// name can exist in two repos.
func TestTrillSendArgsOffersTheLaneAsAClick(t *testing.T) {
	e, row := notifyEnv(t)
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Notification",
		"cwd":             row.Path,
	})
	if !ok {
		t.Fatal("a Notification event must produce a send")
	}
	want := "Go to lane=lane:" + filepath.Base(row.Main) + "/" + row.Name
	i := slices.Index(args, "--action")
	if i < 0 || i+1 >= len(args) || args[i+1] != want {
		t.Fatalf("want --action %q, got %q", want, strings.Join(args, " "))
	}
}

// HOLT_TRILL set is authoritative: pointing at nothing means "no banners",
// never a fall-through to whatever else the machine has.
func TestTrillBinaryHonorsOverride(t *testing.T) {
	t.Setenv("HOLT_TRILL", filepath.Join(t.TempDir(), "absent"))
	if got := trillBinary(); got != "" {
		t.Fatalf("a missing HOLT_TRILL must resolve to nothing, got %q", got)
	}
}
