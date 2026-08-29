//! Wire types for scruff's frozen public contracts — SPEC.md §2.2 (`--json`) and
//! §14.3 step 2 (`watch --json`). Hand-ported from the Go source of truth
//! (`internal/commands/json.go`, `internal/commands/watch.go`) rather than
//! generated, because scruff has no `go generate` step yet for this — see the
//! SDK README for the drift risk that implies. `#[serde(rename = "...")]`
//! and field names are the actual contract; keep them byte-identical to
//! json.go's, not just shaped the same.
//!
//! schema 1 is what's actually implemented today. SPEC.md §2.2's example
//! envelope also shows `pr`, `overlap`, `ahead`, `behind` — those are §0.2/
//! later milestones (`overlap`, forge polling) and are NOT on the wire yet.
//! Do not add them here until json.go does; a field that exists in the type
//! but never arrives on the wire is worse than one that's simply missing.

use serde::{Deserialize, Serialize};

/// scruff's exit-code contract (SPEC.md §2.4). `REFUSED` vs `USAGE` is the one
/// that matters to a caller: "scruff declined to destroy something" vs "you
/// asked wrong".
pub mod exit_code {
    pub const OK: i32 = 0;
    pub const USAGE: i32 = 1;
    pub const REFUSED: i32 = 2;
    pub const DEGRADED: i32 = 3;
    pub const CONFLICT: i32 = 4;
    pub const LOCKED: i32 = 5;
}

/// Known values of [`Lane::state`]. Closed set on the wire today — SPEC.md
/// §2.2: "additions are minor, removals major." `Lane::state` stays a plain
/// `String` rather than a Rust `enum` on purpose: an unrecognized value must
/// round-trip as opaque data, never a decode error.
pub mod lane_state {
    pub const LIVE: &str = "live";
    pub const PARKED: &str = "parked";
    pub const STRAY: &str = "stray";
}

/// Known values of [`Landed::verdict`]. Same open-set discipline as
/// [`lane_state`].
pub mod landed_verdict {
    pub const YES: &str = "yes";
    pub const NO: &str = "no";
    pub const FRESH: &str = "fresh";
    pub const CONTAINED: &str = "contained";
}

/// Known values of [`Landed::via`] (when it's `Some`). Same open-set
/// discipline as [`lane_state`].
pub mod landed_via {
    pub const NEVER_DIVERGED: &str = "never-diverged";
    pub const ANCESTRY: &str = "ancestry";
    pub const PR_HEAD_OID: &str = "pr-head-oid";
    pub const PATCH_EQUIVALENCE: &str = "patch-equivalence";
    pub const MERGE_TREE_EMPTY: &str = "merge-tree-empty";
}

/// How (and how confidently) scruff determined a branch's landed-ness. `via`
/// is `None` when `verdict` didn't need one (e.g. `"no"`).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Landed {
    pub verdict: String,
    pub via: Option<String>,
    pub confidence: String,
}

/// A lane's commit count past an already-merged PR.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PostMergeAhead {
    pub commits: u64,
    /// PR number, or `0` when there isn't one — scruff doesn't null this field
    /// (`internal/commands/json.go`'s `jsonPostMerge`), unlike the envelope-
    /// level `pr`. Treat `0` as "none" here, not as PR #0.
    pub pr: u64,
}

/// One lane, in the exact shape `--json` uses for `lanes[]` — the same shape
/// `watch --json` puts on `event.lane`. One schema whether you're reading a
/// snapshot or a stream (SPEC.md §14.1).
///
/// `occupied` and `dirty` are `Option<bool>` on purpose: `None` means "not
/// determined" (no lsof, no forge, cache miss), which is categorically
/// different from `false`. Every consumer bug in scruff's bash-era statusline
/// came from collapsing that `None` into `false` — do not do that here
/// either.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Lane {
    pub name: String,
    pub repo: String,
    pub main: String,
    pub branch: String,
    pub path: String,
    pub parent: String,
    /// the checkout whose conversation `scruff <name>` opens: this lane's own `path` when it holds a chat, the PARENT's path when the lane is just a checkout somebody's pane edits. Absent or empty means scruff could not tell — read that as "show it". Filter a picker on this, never on `parent`: `parent` cannot tell a `scruff child` checkout from a full lane opened inside another pane, and hiding the second hides a running agent.
    #[serde(default)]
    pub chat: Option<String>,
    /// The client this lane opens (`claude` | `codex` | `opencode` | `pi`, or
    /// whatever adapters are configured) — never the lane's own identity.
    pub agent: String,
    /// See [`lane_state`] for known values.
    pub state: String,
    pub occupied: Option<bool>,
    pub dirty: Option<bool>,
    pub landed: Landed,
    pub post_merge_ahead: PostMergeAhead,
    pub last_commit: String,
}

/// The `scruff --json` / `scruff list --json` envelope — byte-identical between
/// the two spellings (SPEC.md §2.2).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Envelope {
    pub scruff: String,
    pub schema: u32,
    pub lanes: Vec<Lane>,
    pub warnings: Vec<String>,
}

// ---------------------------------------------------------------------------
// `scruff watch --json` — SPEC.md §14.3 step 2, §14.4.

/// Known values of [`WatchLine::kind`]. Closed set, same discipline as
/// [`lane_state`]: additions are minor, removals major. An unknown kind is
/// noise to ignore, not an error to fail on — that's what lets scruff add
/// `landed` / `source: "forge"` later without breaking a build pinned to
/// schema 1 (SPEC.md §14.4).
pub mod watch_kind {
    /// First line of every stream: a version header, not an event.
    pub const HELLO: &str = "hello";
    /// Replays one already-live lane during the initial burst.
    pub const SYNC: &str = "sync";
    /// Marks the end of the sync burst. Names no lane.
    pub const READY: &str = "ready";
    pub const CREATED: &str = "created";
    pub const PARKED: &str = "parked";
    pub const RESUMED: &str = "resumed";
    pub const REAPED: &str = "reaped";
    pub const CHANGED: &str = "changed";
    /// Carries the same text `--json`'s `warnings[]` does, pushed here
    /// because a stream reader has no envelope to poll.
    pub const WARNING: &str = "warning";
}

/// One line of `scruff watch --json`. A single flat struct rather than a
/// tagged union because that is exactly its wire shape — `kind` says which
/// of the other fields are populated, mirroring `encoding/json`'s normal
/// "one struct, some fields empty" idiom rather than forcing a match on
/// every consumer (the same choice the Go SDK's `WatchLine` makes, and for
/// the same reason). At most one lane per line, never a batch.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WatchLine {
    /// See [`watch_kind`] for known values.
    pub kind: String,
    /// Monotonic across the WHOLE stream, hello included — lets a consumer
    /// fanning this out over its own transport (e.g. websockets) detect a
    /// dropped line without scruff knowing anything about that transport.
    pub seq: u64,

    // Hello-only fields.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scruff: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub schema: Option<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub capabilities: Option<Vec<String>>,

    // Event fields (everything but hello).
    /// RFC3339 UTC: when THIS scruff process observed the change, not
    /// necessarily when it happened at the source. Absent on hello.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ts: Option<String>,
    /// Which provider produced the event. v1 only ever writes `"registry"`;
    /// absent on `ready`, which names no lane and no provider. `"forge"` is
    /// reserved for a later milestone (SPEC.md §14.4) — treat it as an
    /// opaque string, not a closed enum.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
    /// Present on every kind except `hello`, `ready` and `warning`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lane: Option<Lane>,
    /// Present only on `warning`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
}

impl WatchLine {
    /// Whether this line is the stream's version header rather than a
    /// lifecycle event.
    pub fn is_hello(&self) -> bool {
        self.kind == watch_kind::HELLO
    }

    /// Narrows a line from [`crate::ScruffClient::watch`] to a [`WatchEvent`],
    /// returning `None` for the one line that isn't an event — the `hello`
    /// header. The Rust spelling of the TS SDK's
    /// `isWatchHello(line) ? … : line as WatchEvent`, for callers of the
    /// full stream who want the narrower type
    /// [`crate::ScruffClient::watch_lane`] hands out.
    pub fn into_event(self) -> Option<WatchEvent> {
        if self.is_hello() {
            return None;
        }
        Some(WatchEvent {
            kind: self.kind,
            seq: self.seq,
            ts: self.ts,
            source: self.source,
            lane: self.lane,
            message: self.message,
        })
    }
}

/// Every line of the stream EXCEPT the `hello` header: the header-only
/// fields (`scruff`, `schema`, `capabilities`) are gone rather than left
/// `None`, so a value of this type cannot be a version header.
/// [`crate::ScruffClient::watch_lane`] yields these — a stream already scoped
/// to one lane never carries a `hello`, and a caller shouldn't have to
/// re-prove that before reading `lane`. Same split the TS/Python SDKs get
/// from `WatchLine = WatchHello | WatchEvent`, and Swift from its
/// `WatchLine` enum.
///
/// `kind` stays a `String` for the same reason [`WatchLine::kind`] does: an
/// unrecognized wire value decodes as opaque data instead of failing. See
/// [`watch_kind`] for known values — `HELLO` never appears here.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WatchEvent {
    pub kind: String,
    /// Monotonic across the WHOLE stream, `hello` included — so the first
    /// event on a filtered stream will not have `seq` 0, and gaps are
    /// expected on a stream this narrow. See [`WatchLine::seq`].
    pub seq: u64,

    /// RFC3339 UTC: when THIS scruff process observed the change, not
    /// necessarily when it happened at the source.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ts: Option<String>,
    /// Which provider produced the event. v1 only ever writes `"registry"`;
    /// absent on `ready`, which names no lane and no provider. Treat it as
    /// an opaque string, not a closed enum.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
    /// Present on every kind except `ready` and `warning`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lane: Option<Lane>,
    /// Present only on `warning`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
}
