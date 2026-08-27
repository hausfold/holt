package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hausfold/scruff/internal/exitcode"
)

// Watch is scruff's lifecycle event stream — one NDJSON object per line on
// stdout, for as long as the process runs. SPEC.md §14.2 names this `onOpen`
// for embedders: every language can read a subprocess pipe, which is why the
// SDKs are built on a stream rather than a callback or a daemon (§14.1).
//
// stdout carries EVENTS ONLY, the same rule as everywhere else in scruff
// (internal/ui's doc comment) — a consumer is parsing this line by line, and
// every diagnostic goes to stderr instead. Nothing here assumes a terminal:
// the first real consumer (SPEC.md §14) is a server holding one `watch` per
// box and fanning its lines out to many websocket sessions, so this command
// must behave identically piped, backgrounded, and under a process
// supervisor as it does run by hand.
//
// # What drives it, and what doesn't
//
// fsnotify watches the registry file, and MOST of the state machine is a
// registry mutation: `created` (registry.Put), `resumed` (Put again, from
// rebuild), `reaped` (Delete/Prune). Watching the file is a free, instant
// signal for those.
//
// `parked` is not, for the common case. `scruff park` only commits — the
// checkout stays live on disk — and the actual live→parked transition is the
// pane CLOSING (HookRemove / `git worktree remove`), which only touches the
// registry when the branch is already landed (to drop its row). An unlanded
// branch — the ordinary "pane closed, work isn't merged yet" case — leaves
// the registry untouched: the row's path doesn't change, so there is nothing
// to rewrite. The state that changed is purely on disk (checkoutState reads
// `.git`'s presence), which fsnotify-on-the-registry structurally cannot see.
// pollInterval below is the backstop for exactly that gap: a plain periodic
// rescan, same cost as one `scruff list` (mostly local git, forge answers
// served from landed.go's disk cache), independent of the registry watch.
//
// `landed` and `post_merge_ahead` are a different gap, and NOT one
// pollInterval papers over: they change at the forge, and nothing local — not
// the registry, not the filesystem — fires when a PR merges. The honest way
// to surface that would be a `gh` poll on its OWN timer, and that is
// deliberately NOT what v1 does: the first consumer is one long-running
// `watch` per box, across however many lanes and repos it's holding leases
// for, and a forge poll baked into the stream multiplies by every one of
// them for as long as the process runs. That is a rate-limit generator, not
// a feature — pollInterval avoids it by hitting only local git and the
// existing disk-cached forge lookup, never issuing a fresh `gh` call on its
// own account. A consumer that wants fresher landedness than the cache still
// polls `scruff --json` at whatever cadence it can afford. A forge-derived
// event family is additive later — new `kind` values, `source: "forge"` on
// the events that carry them — and every field that choice needs (`source`,
// `capabilities`) is already in this schema so that day doesn't cost a
// schema bump. See watchEvent's doc comment.
func (e *Env) Watch(args []string) error {
	for _, a := range args {
		// Accepted for spelling symmetry with `scruff list --json` — watch has
		// no other output mode, so this is a no-op, not a switch.
		if a == "--json" {
			continue
		}
		return exitcode.Usagef("unknown flag %q — try `scruff watch --json`", a)
	}

	regDir := filepath.Dir(e.Reg.Path())
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		return err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	if err := fw.Add(regDir); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	seq := 0
	// Both helpers share the same counter, hello included — see watchEvent's
	// doc comment on why seq spans the whole stream rather than resetting
	// after the header line.
	emit := func(ev watchEvent) error {
		ev.Seq = seq
		seq++
		ev.TS = time.Now().UTC().Format(time.RFC3339)
		return enc.Encode(ev)
	}
	emitHello := func(h watchHello) error {
		h.Seq = seq
		seq++
		return enc.Encode(h)
	}

	if err := emitHello(watchHello{
		Kind:         "hello",
		Scruff:       Version,
		Schema:       2,
		Capabilities: []string{"registry"},
	}); err != nil {
		return err
	}

	prev := map[string]jsonLane{}
	rescan := func(newKind string) error {
		occ := e.Occupancy()
		cur := map[string]jsonLane{}
		for _, r := range e.rows() {
			cur[r.Entry.Path] = e.toJSONLane(r, occ)
		}
		for _, le := range diffLanes(prev, cur, newKind) {
			lane := le.Lane
			if err := emit(watchEvent{Kind: le.Kind, Source: "registry", Lane: &lane}); err != nil {
				return err
			}
		}
		prev = cur
		return nil
	}

	if err := rescan("sync"); err != nil {
		return err
	}
	if err := emit(watchEvent{Kind: "ready"}); err != nil {
		return err
	}

	// SIGTERM is how a supervising process — the web-server consumer SPEC.md
	// §14 describes — ends a long-running child; SIGINT is Ctrl-C at a
	// terminal. Both mean "stop cleanly", never "something broke": watch has
	// nothing to flush beyond stdout's own buffering, so there is nothing to
	// do here beyond returning before the next rescan starts.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	// The filesystem-only backstop — see the package doc comment for the gap
	// this covers (an unlanded pane closing never touches the registry).
	// Ticks are independent of the registry watch/debounce below; a rescan is
	// idempotent (diffLanes emits nothing when nothing changed), so an
	// overlapping tick and registry event just do the same work twice rather
	// than racing.
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	regBase := filepath.Base(e.Reg.Path())
	var debounce *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			base := filepath.Base(ev.Name)
			// mutate() writes through a `.registry-*` temp file and renames it
			// onto registry.tsv (registry.go), so both names are the same
			// logical event. Everything else in this directory (the lock
			// file, the forge cache) is noise `watch` has no opinion about.
			if base != regBase && !strings.HasPrefix(base, ".registry-") {
				continue
			}
			// Coalesce a burst of writes (mutate's temp-file-then-rename is
			// two fsnotify events on its own) into one rescan, so a run of
			// registry ops close together — the sweep pruning several rows —
			// reads as one settled diff rather than a flurry of half-updates.
			if debounce == nil {
				debounce = time.AfterFunc(watchDebounce, func() { fire <- struct{}{} })
			} else {
				debounce.Reset(watchDebounce)
			}
		case werr, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			if err := emit(watchEvent{Kind: "warning", Message: fmt.Sprintf("watch: %v", werr)}); err != nil {
				return err
			}
		case <-fire:
			if err := rescan("created"); err != nil {
				return err
			}
		case <-poll.C:
			if err := rescan("created"); err != nil {
				return err
			}
		case <-sig:
			return nil
		}
	}
}

// watchDebounce absorbs a registry mutation's temp-write-then-rename (always
// two fsnotify events for one logical change) plus several lanes changing in
// the same sweep, into a single settled rescan.
const watchDebounce = 200 * time.Millisecond

// pollInterval is the filesystem-only backstop's cadence — see Watch's doc
// comment for what it covers and why it's cheap. 3s is fast enough that a
// pane closing reads as a lifecycle event within one human "huh, did that
// register?" pause, and slow enough that it costs nothing: each tick is one
// `e.rows()`, the same call `scruff list` makes, and landed.go's disk cache
// (120s TTL) means the overwhelming majority of ticks issue zero forge calls.
const pollInterval = 3 * time.Second

// watchHello is the first line of every stream — a version header so a
// consumer can check compatibility once, up front, rather than sniffing the
// first event. Same rationale as the --json envelope's {scruff, schema} pair
// (SPEC.md §2.2), and deliberately the same two field names.
//
// capabilities exists so a consumer can ask "will this stream ever emit a
// forge-derived event?" instead of guessing from which kinds happen to have
// shown up yet, or hardcoding a kind list that a later scruff version might
// extend. v1 always sends exactly one value, "registry"; the day source
// "forge" exists, its build advertises "forge" here too — additively, no
// schema bump.
type watchHello struct {
	Kind         string   `json:"kind"` // always "hello"
	Seq          int      `json:"seq"`
	Scruff       string   `json:"scruff"`
	Schema       int      `json:"schema"`
	Capabilities []string `json:"capabilities"`
}

// watchEvent is every line after hello. One line, at most one lane — never a
// batch — so a consumer's line-reader has exactly one shape to handle whether
// it's draining the initial sync or watching a live change.
//
// kind is a closed set, the same discipline jsonLane.State and Landed.Verdict
// already hold in the --json envelope: additions are minor, removals are
// major, and a consumer must treat an unknown kind as noise rather than guess
// at its meaning.
//
//	sync     one line per lane already alive when the stream opened — a
//	         consumer's baseline. Always followed by exactly one "ready".
//	ready    the sync burst is over; everything from here is a live change.
//	created  a lane exists that didn't a moment ago
//	parked   live → parked
//	resumed  parked → live
//	reaped   a lane that existed no longer does — swept, or hand-removed
//	changed  same identity, still present, something else about it differs
//	         from the last line this stream sent for it: agent, dirty,
//	         landed, post_merge_ahead, or last_commit
//	warning  a degraded-mode line — the same messages `warnings[]` carries
//	         under --json, pushed here because a stream reader has no
//	         envelope to poll for them
//
// seq is a monotonic counter over the WHOLE stream — hello included, every
// kind consumes one — so a consumer fanning this out over its own transport
// (websockets, for the server SPEC.md §14 names as the first real consumer)
// can detect a dropped line without scruff knowing anything about that
// transport. ts is when THIS scruff process observed the change, not when it
// happened at the source — for a registry mutation those are the same thing
// to within the debounce window; they will not be, later, for a forge event.
//
// source names which provider produced the event. v1 only ever writes
// "registry" — see the package doc comment above Watch for why forge-derived
// events are out of scope for this pass — but the field is here now so a
// later `kind` (say, "landed") can carry `source: "forge"` and be
// distinguishable from day one, without every consumer needing a schema bump
// to tell the two families apart. Absent on "ready", which names no lane and
// no provider.
type watchEvent struct {
	Kind    string    `json:"kind"`
	Seq     int       `json:"seq"`
	TS      string    `json:"ts"`
	Source  string    `json:"source,omitempty"`
	Lane    *jsonLane `json:"lane,omitempty"`
	Message string    `json:"message,omitempty"`
}

// laneEvent is diffLanes' pure-function return shape: a kind and the lane it
// is about, before either has been wrapped in an envelope or given a seq/ts.
// Keeping it separate from watchEvent is what makes diffLanes testable
// without a stream, a stdout encoder, or fsnotify anywhere nearby.
type laneEvent struct {
	Kind string
	Lane jsonLane
}

// diffLanes compares two full snapshots — keyed by checkout path, the
// registry's own primary key — and returns one event per lane whose derived
// state changed, sorted by path so a rescan that touches several lanes at
// once emits them in a stable order rather than at map-iteration's mercy.
//
// newKind names what an entirely new path counts as: "sync" for the very
// first rescan a stream ever does (that is a baseline being reported, not
// something that just happened) and "created" for every rescan after.
//
// Comparison is reflect.DeepEqual, not ==: jsonLane carries *bool fields
// (Occupied, Dirty), and every rescan builds a fresh jsonLane with its own
// pointers even when the value underneath hasn't moved — a plain != would
// compare those addresses and call every lane "changed" on every single
// rescan, which is the bug this call sidesteps.
func diffLanes(prev, cur map[string]jsonLane, newKind string) []laneEvent {
	var events []laneEvent

	paths := make([]string, 0, len(cur))
	for p := range cur {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		lane := cur[p]
		old, existed := prev[p]
		switch {
		case !existed:
			events = append(events, laneEvent{Kind: newKind, Lane: lane})
		case old.State == string(Parked) && lane.State == string(Live):
			events = append(events, laneEvent{Kind: "resumed", Lane: lane})
		case old.State == string(Live) && lane.State == string(Parked):
			events = append(events, laneEvent{Kind: "parked", Lane: lane})
		case !reflect.DeepEqual(old, lane):
			events = append(events, laneEvent{Kind: "changed", Lane: lane})
		}
	}

	var gone []string
	for p := range prev {
		if _, still := cur[p]; !still {
			gone = append(gone, p)
		}
	}
	sort.Strings(gone)
	for _, p := range gone {
		events = append(events, laneEvent{Kind: "reaped", Lane: prev[p]})
	}
	return events
}
