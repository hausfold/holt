package holt

// Wire types for holt's frozen public contracts — SPEC.md §2.2 (`--json`) and
// §14.3 step 2 (`watch --json`). Hand-ported from the Go source of truth
// (internal/commands/json.go, internal/commands/watch.go) rather than
// generated, because holt has no `go generate` step yet for this — see the
// SDK README for the drift risk that implies. Struct tags are the actual
// contract; keep them byte-identical to json.go's, not just field names.
//
// schema 1 is what's actually implemented today. SPEC.md §2.2's example
// envelope also shows `pr`, `overlap`, `ahead`, `behind` — those are §0.2/
// later milestones (`overlap`, forge polling) and are NOT on the wire yet.
// Do not add them here until json.go does; a field that exists in the type
// but never arrives on the wire is worse than one that's simply missing.

// ExitCode is holt's exit-code contract (SPEC.md §2.4). Refused vs Usage is
// the one that matters to a caller: "I declined to destroy something" vs
// "you asked wrong".
type ExitCode int

const (
	ExitOK       ExitCode = 0
	ExitUsage    ExitCode = 1
	ExitRefused  ExitCode = 2
	ExitDegraded ExitCode = 3
	ExitConflict ExitCode = 4
	ExitLocked   ExitCode = 5
)

// LaneState is a lane's lifecycle state. Closed set — SPEC.md §2.2:
// "additions are minor, removals major." An unrecognized value round-trips
// as a plain string, not a decode error — treat it as opaque, never panic
// or reject on one you don't recognize.
type LaneState string

const (
	LaneLive   LaneState = "live"
	LaneParked LaneState = "parked"
	LaneStray  LaneState = "stray"
)

// LandedVerdict is the closed set §2.2 defines for Landed.Verdict.
type LandedVerdict string

const (
	LandedYes       LandedVerdict = "yes"
	LandedNo        LandedVerdict = "no"
	LandedContained LandedVerdict = "contained"
)

// Landed carries how (and how confidently) holt determined a branch's
// landed-ness. Via is empty when Verdict didn't need one (e.g. "no").
type Landed struct {
	Verdict    LandedVerdict `json:"verdict"`
	Via        string        `json:"via"`
	Confidence string        `json:"confidence"`
}

// PostMergeAhead is a lane's commit count past an already-merged PR.
type PostMergeAhead struct {
	Commits int `json:"commits"`
	// PR is the pull request number, or 0 when there isn't one — holt
	// doesn't null this field (internal/commands/json.go's jsonPostMerge),
	// unlike Envelope-level `pr`. Treat 0 as "none" here, not as PR #0.
	PR int `json:"pr"`
}

// Lane is one lane, in the exact shape `--json` uses for `lanes[]` — the
// same shape `watch --json` puts on an event's `lane`. One schema whether
// you're reading a snapshot or a stream (SPEC.md §14.1).
//
// Occupied and Dirty are *bool on purpose: nil means "not determined" (no
// lsof, no forge, cache miss), which is categorically different from
// false. Every consumer bug in holt's bash-era statusline came from
// collapsing that nil into false — do not do that here either.
type Lane struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Main   string `json:"main"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Parent string `json:"parent"`
	// Agent is the client this lane opens (claude | codex | opencode, or
	// whatever adapters are configured) — never the lane's own identity.
	Agent          string         `json:"agent"`
	State          LaneState      `json:"state"`
	Occupied       *bool          `json:"occupied"`
	Dirty          *bool          `json:"dirty"`
	Landed         Landed         `json:"landed"`
	PostMergeAhead PostMergeAhead `json:"post_merge_ahead"`
	LastCommit     string         `json:"last_commit"`
}

// Envelope is the `holt --json` / `holt list --json` payload — byte-
// identical between the two spellings (SPEC.md §2.2).
type Envelope struct {
	Holt     string   `json:"holt"`
	Schema   int      `json:"schema"`
	Lanes    []Lane   `json:"lanes"`
	Warnings []string `json:"warnings"`
}

// ---------------------------------------------------------------------------
// `holt watch --json` — SPEC.md §14.3 step 2, §14.4.

// WatchKind is the closed set of lines a watch stream can emit. Additions
// are minor, removals major — same discipline as LaneState. An unknown
// kind is noise to ignore, not an error to fail on; that's what lets holt
// add `landed` / `source: "forge"` later without breaking a Go build
// pinned to schema 1 (SPEC.md §14.4).
type WatchKind string

const (
	// WatchHello is the first line of every stream: a version header, not
	// an event. Only Kind, Seq, Holt, Schema and Capabilities are set.
	WatchHello WatchKind = "hello"
	// WatchSync replays one already-live lane during the initial burst.
	WatchSync WatchKind = "sync"
	// WatchReady marks the end of the sync burst. Names no lane.
	WatchReady   WatchKind = "ready"
	WatchCreated WatchKind = "created"
	WatchParked  WatchKind = "parked"
	WatchResumed WatchKind = "resumed"
	WatchReaped  WatchKind = "reaped"
	WatchChanged WatchKind = "changed"
	// WatchWarning carries the same text `--json`'s `warnings[]` does,
	// pushed here because a stream reader has no envelope to poll.
	WatchWarning WatchKind = "warning"
)

// WatchLine is one line of `holt watch --json`. It is a single flat struct
// rather than a tagged union because that is exactly its wire shape — Kind
// says which of the other fields are populated, mirroring
// encoding/json's normal "one struct, some fields empty" idiom rather than
// forcing a type switch on every consumer. At most one lane per line, never
// a batch.
type WatchLine struct {
	Kind WatchKind `json:"kind"`
	// Seq is monotonic across the WHOLE stream, hello included — lets a
	// consumer fanning this out over its own transport (e.g. websockets)
	// detect a dropped line without holt knowing anything about that
	// transport.
	Seq int `json:"seq"`

	// Hello-only fields.
	Holt         string   `json:"holt,omitempty"`
	Schema       int      `json:"schema,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`

	// Event fields (everything but hello).
	//
	// Ts is RFC3339 UTC: when THIS holt process observed the change, not
	// necessarily when it happened at the source. Absent on hello.
	Ts string `json:"ts,omitempty"`
	// Source names which provider produced the event. v1 only ever writes
	// "registry"; absent on ready, which names no lane and no provider.
	// "forge" is reserved for a later milestone (SPEC.md §14.4) — treat it
	// as an opaque string, not a closed enum.
	Source string `json:"source,omitempty"`
	// Lane is present on every kind except hello, ready and warning.
	Lane *Lane `json:"lane,omitempty"`
	// Message is present only on warning.
	Message string `json:"message,omitempty"`
}

// IsHello reports whether this line is the stream's version header rather
// than a lifecycle event.
func (l WatchLine) IsHello() bool { return l.Kind == WatchHello }
