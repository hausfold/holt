package holt_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	holt "github.com/hausfold/holt/sdk/go"
)

// testdata/fake-holt.sh stands in for the real binary so tests don't need
// a Go build of holt itself — it's a fixture, not a spec of holt's
// behavior. Shared verbatim with sdk/ts and sdk/python's fixture of the
// same name; keep the three in sync if the wire protocol changes.
func newClient(t *testing.T) *holt.Client {
	t.Helper()
	return &holt.Client{Bin: "./testdata/fake-holt.sh"}
}

func TestList(t *testing.T) {
	env, err := newClient(t).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if env.Schema != 1 {
		t.Errorf("Schema = %d, want 1", env.Schema)
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
	if frost.Landed.Verdict != holt.LandedContained {
		t.Errorf("frost.Landed.Verdict = %q, want contained", frost.Landed.Verdict)
	}
}

func TestWatch_YieldsHelloSyncReadyThenStopsOnBreak(t *testing.T) {
	var kinds []holt.WatchKind
	for line, err := range newClient(t).Watch(context.Background()) {
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}
		kinds = append(kinds, line.Kind)
		if line.Kind == holt.WatchCreated {
			break
		}
	}
	want := []holt.WatchKind{holt.WatchHello, holt.WatchSync, holt.WatchReady, holt.WatchCreated}
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
	var seen []holt.WatchEvent
	// The element type is WatchEvent, not WatchLine — a lane-scoped stream
	// has no hello to carry, so it doesn't hand out a type that could be
	// one. This assignment is half the assertion: it stops compiling if
	// WatchLane ever widens back.
	var stream iter.Seq2[holt.WatchEvent, error] = newClient(t).WatchLane(context.Background(), "/repo/.holt/haus/fresh")
	for event, err := range stream {
		if err != nil {
			t.Fatalf("WatchLane: %v", err)
		}
		seen = append(seen, event)
		break
	}
	if len(seen) != 1 || seen[0].Kind != holt.WatchCreated {
		t.Fatalf("seen = %v, want one created event", seen)
	}
	if seen[0].Lane == nil || seen[0].Lane.Path != "/repo/.holt/haus/fresh" {
		t.Errorf("seen[0].Lane = %v, want the fresh lane", seen[0].Lane)
	}
}

func TestWatchLine_EventNarrowsEverythingButHello(t *testing.T) {
	hello := holt.WatchLine{Kind: holt.WatchHello, Seq: 0, Holt: "0.1.0", Schema: 1}
	if _, ok := hello.Event(); ok {
		t.Error("hello.Event() ok = true, want false — the header is not an event")
	}

	line := holt.WatchLine{
		Kind:   holt.WatchCreated,
		Seq:    4,
		Ts:     "2026-08-08T12:00:00Z",
		Source: "registry",
		Lane:   &holt.Lane{Name: "fresh", Path: "/repo/.holt/haus/fresh"},
	}
	event, ok := line.Event()
	if !ok {
		t.Fatal("created.Event() ok = false, want true")
	}
	if event.Kind != holt.WatchCreated || event.Seq != 4 || event.Ts != line.Ts || event.Source != "registry" {
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
	if dir != "/repo/.holt/other/new-lane" {
		t.Errorf("dir = %q, want /repo/.holt/other/new-lane", dir)
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
	lease := c.Lease(context.Background(), "/repo/.holt/haus/sparkle", holt.LeaseOptions{PID: 12345})
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err) // fake-holt's heartbeat branch accepts --release silently
	}
}
