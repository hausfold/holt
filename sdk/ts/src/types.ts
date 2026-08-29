// Wire types for scruff's frozen public contracts — SPEC.md §2.2 (`--json`) and
// §14.3 step 2 (`watch --json`). Hand-ported from the Go source of truth
// (internal/commands/json.go, internal/commands/watch.go) rather than
// generated, because scruff has no `go generate` step yet for this — see the
// SDK README for the drift risk that implies.
//
// schema 1 is what's actually implemented today. SPEC.md §2.2's example
// envelope also shows `pr`, `overlap`, `ahead`, `behind` — those are §0.2/
// later milestones (`overlap`, forge polling) and are NOT on the wire yet.
// Do not add them here until json.go does; a field that exists in the type
// but never arrives on the wire is worse than one that's simply missing.

/** scruff's exit-code contract (SPEC.md §2.4). `2` vs `1` is the one that
 * matters to a caller: "you asked wrong" vs "I declined to destroy
 * something". */
export enum ScruffExitCode {
  OK = 0,
  Usage = 1,
  Refused = 2,
  Degraded = 3,
  Conflict = 4,
  Locked = 5,
}

/** A lane's lifecycle state. Closed set — SPEC.md §2.2: "additions are
 * minor, removals major." Treat an unknown value as opaque, not an error. */
export type LaneState = "live" | "parked" | "stray";

export type LandedVerdict = "yes" | "no" | "fresh" | "contained";

export type LandedVia =
  | "never-diverged"
  | "ancestry"
  | "pr-head-oid"
  | "patch-equivalence"
  | "merge-tree-empty"
  | null;

export interface LandedInfo {
  verdict: LandedVerdict;
  via: LandedVia;
  confidence: string;
}

export interface PostMergeAhead {
  commits: number;
  /** PR number, or 0 when there isn't one — scruff doesn't null this field
   * today (internal/commands/json.go's jsonPostMerge), unlike `pr` at the
   * envelope level. Treat `0` as "none" here, not as PR #0. */
  pr: number;
}

/**
 * One lane, in the exact shape `--json` uses for `lanes[]` — the same shape
 * `watch --json` puts on `event.lane`. One schema whether you're reading a
 * snapshot or a stream (SPEC.md §14.1).
 *
 * `occupied` and `dirty` are three-state on purpose: `null` means "not
 * determined" (no lsof, no forge, cache miss), which is categorically
 * different from `false`. Every consumer bug in scruff's bash-era statusline
 * came from collapsing that `null` into `false` — do not do that here either.
 */
export interface ScruffLane {
  name: string;
  repo: string;
  main: string;
  branch: string;
  path: string;
  parent: string;
  /** `chat` — the checkout whose conversation `scruff <name>` opens: this lane's own `path` when it holds a chat, the PARENT's path when the lane is just a checkout somebody's pane edits. Absent or empty means scruff could not tell — read that as "show it". Filter a picker on this, never on `parent`: `parent` cannot tell a `scruff child` checkout from a full lane opened inside another pane, and hiding the second hides a running agent. */
  chat?: string;
  /** The client this lane opens (`claude` | `codex` | `opencode` | `pi`, or
   * whatever adapters are configured) — never the lane's own identity. */
  agent: string;
  state: LaneState;
  occupied: boolean | null;
  dirty: boolean | null;
  landed: LandedInfo;
  post_merge_ahead: PostMergeAhead;
  last_commit: string;
}

/** The `scruff --json` / `scruff list --json` envelope — byte-identical between
 * the two spellings (SPEC.md §2.2). */
export interface ScruffEnvelope {
  scruff: string;
  schema: number;
  lanes: ScruffLane[];
  warnings: string[];
}

// ---------------------------------------------------------------------------
// `scruff watch --json` — SPEC.md §14.3 step 2, §14.4.

/** First line of every `watch` stream. A version header, not an event — see
 * `capabilities` below for why it carries more than `{scruff, schema}`. */
export interface WatchHello {
  kind: "hello";
  seq: number;
  scruff: string;
  schema: number;
  /** What families of event this scruff build can ever send on this stream.
   * v1 always sends exactly `["registry"]`; a future `"forge"` entry is how
   * a consumer learns a `landed`/`post_merge_ahead` event kind might show up
   * without guessing from which kinds happen to have arrived yet. */
  capabilities: string[];
}

/** Closed set, same discipline as `LaneState` and `LandedVerdict`: additions
 * are minor, removals major. An unknown kind is noise to ignore, not an
 * error to throw on — that's what lets scruff add `landed`/`source: "forge"`
 * later without breaking every SDK pinned to v1 (SPEC.md §14.4). */
export type WatchEventKind =
  | "sync"
  | "ready"
  | "created"
  | "parked"
  | "resumed"
  | "reaped"
  | "changed"
  | "warning";

/** Every line after `hello`. One line, at most one lane — never a batch. */
export interface WatchEvent {
  kind: WatchEventKind;
  /** Monotonic across the WHOLE stream, hello included — lets a consumer
   * fanning this out over its own transport (e.g. websockets) detect a
   * dropped line without scruff knowing anything about that transport. */
  seq: number;
  /** RFC3339 UTC. When THIS scruff process observed the change, not
   * necessarily when it happened at the source. Absent on `hello`. */
  ts?: string;
  /** Which provider produced the event. v1 only ever writes `"registry"`;
   * absent on `ready`, which names no lane and no provider. */
  source?: "registry" | "forge" | string;
  /** Present on every kind except `ready` and `warning`. */
  lane?: ScruffLane;
  /** Present only on `warning` — the same text `warnings[]` carries under
   * `--json`, pushed here because a stream reader has no envelope to poll. */
  message?: string;
}

export type WatchLine = WatchHello | WatchEvent;

export function isWatchHello(line: WatchLine): line is WatchHello {
  return line.kind === "hello";
}
