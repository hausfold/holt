package scruff_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	scruff "github.com/hausfold/scruff/sdk/go"
)

// testdata/fake-scruff.sh stands in for the real binary so tests don't need
// a Go build of scruff itself — it's a fixture, not a spec of scruff's
// behavior. Shared verbatim with sdk/ts and sdk/python's fixture of the
// same name; keep the three in sync if the wire protocol changes.
func newClient(t *testing.T) *scruff.Client {
	t.Helper()
	return &scruff.Client{Bin: "./testdata/fake-scruff.sh"}
}

func TestList(t *testing.T) {
	env, err := newClient(t).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if env.Schema != 2 {
		t.Errorf("Schema = %d, want 2", env.Schema)
	}
	if len(env.Lanes) != 2 {
		t.Fatalf("len(Lanes) = %d, want 2", len(env.Lanes))
	}

	sparkle := env.Lanes[0]
	if sparkle.Occupied == nil || !*sparkle.Occupied {
		t.Errorf("sparkle.Occupied = %v, want true", sparkle.Occupied)
	}
	if sparkle.Dirty == nil || *sparkle.Dirty {
		t.Errorf("sparkle.Dirty = %v, want false (not nil)", sparkle.Dirty)
	}

	frost := env.Lanes[1]
	if frost.Occupied != nil {
		t.Errorf("frost.Occupied = %v, want nil (not determined)", *frost.Occupied)
	}
	if frost.Dirty != nil {
		t.Errorf("frost.Dirty = %v, want nil", *frost.Dirty)
	}
	if frost.Landed.Verdict != scruff.LandedContained {
		t.Errorf("frost.Landed.Verdict = %q, want contained", frost.Landed.Verdict)
	}
}

func TestWatch_YieldsHelloSyncReadyThenStopsOnBreak(t *testing.T) {
	var kinds []scruff.WatchKind
	for line, err := range newClient(t).Watch(context.Background()) {
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}
		kinds = append(kinds, line.Kind)
		if line.Kind == scruff.WatchCreated {
			break
		}
	}
	want := []scruff.WatchKind{scruff.WatchHello, scruff.WatchSync, scruff.WatchReady, scruff.WatchCreated}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("kinds[%d] = %q, want %q", i, kinds[i], want[i])
		}
	}
}

func TestWatchLane_FiltersToOneLane(t *testing.T) {
	var seen []scruff.WatchEvent
	// The element type is WatchEvent, not WatchLine — a lane-scoped stream
	// has no hello to carry, so it doesn't hand out a type that could be
	// one. This assignment is half the assertion: it stops compiling if
	// WatchLane ever widens back.
	var stream iter.Seq2[scruff.WatchEvent, error] = newClient(t).WatchLane(context.Background(), "/repo/.scruff/haus/fresh")
	for event, err := range stream {
		if err != nil {
			t.Fatalf("WatchLane: %v", err)
		}
		seen = append(seen, event)
		break
	}
	if len(seen) != 1 || seen[0].Kind != scruff.WatchCreated {
		t.Fatalf("seen = %v, want one created event", seen)
	}
	if seen[0].Lane == nil || seen[0].Lane.Path != "/repo/.scruff/haus/fresh" {
		t.Errorf("seen[0].Lane = %v, want the fresh lane", seen[0].Lane)
	}
}

func TestWatchLine_EventNarrowsEverythingButHello(t *testing.T) {
	hello := scruff.WatchLine{Kind: scruff.WatchHello, Seq: 0, Scruff: "0.1.0", Schema: 2}
	if _, ok := hello.Event(); ok {
		t.Error("hello.Event() ok = true, want false — the header is not an event")
	}

	line := scruff.WatchLine{
		Kind:   scruff.WatchCreated,
		Seq:    4,
		Ts:     "2026-08-08T12:00:00Z",
		Source: "registry",
		Lane:   &scruff.Lane{Name: "fresh", Path: "/repo/.scruff/haus/fresh"},
	}
	event, ok := line.Event()
	if !ok {
		t.Fatal("created.Event() ok = false, want true")
	}
	if event.Kind != scruff.WatchCreated || event.Seq != 4 || event.Ts != line.Ts || event.Source != "registry" {
		t.Errorf("event = %+v, want the line's own scalars", event)
	}
	if event.Lane != line.Lane {
		t.Errorf("event.Lane = %v, want the same lane pointer", event.Lane)
	}
}

func TestChild_ReturnsOnlyTheNewPath(t *testing.T) {
	dir, err := newClient(t).Child(context.Background(), "/repo/other", "")
	if err != nil {
		t.Fatalf("Child: %v", err)
	}
	if dir != "/repo/.scruff/other/new-lane" {
		t.Errorf("dir = %q, want /repo/.scruff/other/new-lane", dir)
	}
}

func TestResume_CapturedStdoutNeverExecs(t *testing.T) {
	out, err := newClient(t).Resume(context.Background(), "sparkle")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !strings.Contains(out, "claude --resume") {
		t.Errorf("out = %q, want it to contain %q", out, "claude --resume")
	}
}

func TestLease_ReleaseCallsHeartbeatRelease(t *testing.T) {
	c := newClient(t)
	lease := c.Lease(context.Background(), "/repo/.scruff/haus/sparkle", scruff.LeaseOptions{PID: 12345})
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err) // fake-scruff's heartbeat branch accepts --release silently
	}
}
