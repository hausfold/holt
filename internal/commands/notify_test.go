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

// A fin nothing can name again is a fin that stacks: two permission prompts
// from one lane would hang two, and the ledge holds five.
func TestTrillSendArgsKeysTheFinByLane(t *testing.T) {
	e, row := notifyEnv(t)
	want := "holt/" + filepath.Base(row.Main) + "/" + row.Name
	for _, event := range []string{"Notification", "Stop"} {
		args, ok := e.trillSendArgs(map[string]any{
			"hook_event_name": event, "cwd": row.Path, "session_id": "abc-123",
		})
		if !ok {
			t.Fatalf("%s must produce a send", event)
		}
		i := slices.Index(args, "--key")
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Fatalf("%s: want --key %q, got %q", event, want, strings.Join(args, " "))
		}
	}
}

// A pane outside every lane has no lane identity to key by — and its directory
// is not one either, since a session can cd out of it. The client's session id
// is the only stable name it has.
func TestTrillSendArgsKeysANonLanePaneBySession(t *testing.T) {
	e, _ := notifyEnv(t)
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Notification", "cwd": "/somewhere/else/mytool",
		"session_id": "abc-123",
	})
	if !ok {
		t.Fatal("a Notification event must produce a send")
	}
	i := slices.Index(args, "--key")
	if i < 0 || i+1 >= len(args) || args[i+1] != "holt/session/abc-123" {
		t.Fatalf("want the session key, got %q", strings.Join(args, " "))
	}
}

// Nothing to key by at all (an older client, no session id) is not a failure:
// the banner still goes up, it just can't be resolved later.
func TestTrillSendArgsOmitsTheKeyWhenThereIsNothingToName(t *testing.T) {
	e, _ := notifyEnv(t)
	args, ok := e.trillSendArgs(map[string]any{
		"hook_event_name": "Stop", "cwd": "/somewhere/else/mytool",
	})
	if !ok || slices.Contains(args, "--key") {
		t.Fatalf("want no --key, got %q", strings.Join(args, " "))
	}
}

// The gate the resume events read. Its whole job is to be cheap and honest:
// nothing outstanding anywhere → no registry read, no trill launch.
func TestAskMarkersGateTheResolvePath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOLT_STATE", "")
	if anyAskOutstanding() {
		t.Fatal("a fresh state dir has no asks outstanding")
	}
	markAskOutstanding("holt/alpha/sparkle")
	if !anyAskOutstanding() {
		t.Fatal("a marked ask must be outstanding")
	}
	if clearAskOutstanding("holt/alpha/other") {
		t.Fatal("clearing another lane's key must report nothing cleared")
	}
	if !clearAskOutstanding("holt/alpha/sparkle") {
		t.Fatal("clearing the marked key must report it cleared")
	}
	// Idempotent: a fin dismissed by hand leaves nothing behind to clear twice.
	if clearAskOutstanding("holt/alpha/sparkle") || anyAskOutstanding() {
		t.Fatal("a cleared ask must stay cleared")
	}
}

// Keys become one filename each, and a key with a separator in it may not
// climb out of the state dir.
func TestAskMarkerStaysInsideTheStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOLT_STATE", "")
	for _, key := range []string{"holt/alpha/sparkle", "holt/../../etc/passwd"} {
		if got := filepath.Dir(askMarker(key)); got != asksDir() {
			t.Fatalf("key %q escaped to %q", key, got)
		}
	}
}
