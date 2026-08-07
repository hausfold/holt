// Wire types for holt's frozen public contracts — SPEC.md §2.2 (`--json`) and
// §14.3 step 2 (`watch --json`). Hand-ported from the Go source of truth
// (internal/commands/json.go, internal/commands/watch.go), same as the TS
// and Python SDKs — see this package's README for the drift risk that
// implies.
//
// schema 1 is what's actually implemented today. SPEC.md §2.2's example
// envelope also shows `pr`, `overlap`, `ahead`, `behind` — those are §0.2/
// later milestones (`overlap`, forge polling) and are NOT on the wire yet.
// Do not add them here until json.go does; a field that exists in the type
// but never arrives on the wire is worse than one that's simply missing.

import Foundation

/// holt's exit-code contract (SPEC.md §2.4). `.refused` vs `.usage` is the
/// one that matters to a caller: "you asked wrong" vs "I declined to
/// destroy something".
public enum HoltExitCode: Int32, Sendable {
    case ok = 0
    case usage = 1
    case refused = 2
    case degraded = 3
    case conflict = 4
    case locked = 5
}

/// An extensible, string-backed "closed set" — SPEC.md §2.2's discipline:
/// "additions are minor, removals major." Unlike a Swift `enum`, decoding an
/// unrecognized value never throws; it just becomes a `HoltOpenEnum` whose
/// `rawValue` doesn't match any of the `static let` cases. That mirrors the
/// TS/Python SDKs, where an unknown string flows through untouched rather
/// than blowing up JSON parsing — the same leniency that lets holt add
/// `landed`/`source: "forge"` later without breaking a pinned consumer
/// (SPEC.md §14.4). `Tag` is a phantom type purely to keep e.g. `LaneState`
/// and `LandedVerdict` from being interchangeable despite sharing a
/// representation.
public struct HoltOpenEnum<Tag>: RawRepresentable, Hashable, Codable, Sendable {
    public let rawValue: String

    public init(rawValue: String) {
        self.rawValue = rawValue
    }

    public init(from decoder: Decoder) throws {
        rawValue = try decoder.singleValueContainer().decode(String.self)
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(rawValue)
    }
}

extension HoltOpenEnum: CustomStringConvertible {
    public var description: String { rawValue }
}

public enum LaneStateTag {}
/// A lane's lifecycle state. Closed set — SPEC.md §2.2: "additions are
/// minor, removals major." Treat an unrecognized value as opaque, not an
/// error.
public typealias LaneState = HoltOpenEnum<LaneStateTag>
extension LaneState {
    public static let live = LaneState(rawValue: "live")
    public static let parked = LaneState(rawValue: "parked")
    public static let stray = LaneState(rawValue: "stray")
}

public enum LandedVerdictTag {}
public typealias LandedVerdict = HoltOpenEnum<LandedVerdictTag>
extension LandedVerdict {
    public static let yes = LandedVerdict(rawValue: "yes")
    public static let no = LandedVerdict(rawValue: "no")
    public static let contained = LandedVerdict(rawValue: "contained")
}

public enum LandedViaTag {}
/// `nil` on the wire means holt has no verdict path to report — see
/// `HoltLane.landed`. That's distinct from an unrecognized non-null string,
/// which still round-trips as an opaque `LandedVia`.
public typealias LandedVia = HoltOpenEnum<LandedViaTag>
extension LandedVia {
    public static let ancestry = LandedVia(rawValue: "ancestry")
    public static let prHeadOID = LandedVia(rawValue: "pr-head-oid")
    public static let patchEquivalence = LandedVia(rawValue: "patch-equivalence")
    public static let mergeTreeEmpty = LandedVia(rawValue: "merge-tree-empty")
}

public struct LandedInfo: Codable, Hashable, Sendable {
    public let verdict: LandedVerdict
    public let via: LandedVia?
    public let confidence: String

    public init(verdict: LandedVerdict, via: LandedVia?, confidence: String) {
        self.verdict = verdict
        self.via = via
        self.confidence = confidence
    }
}

public struct PostMergeAhead: Codable, Hashable, Sendable {
    public let commits: Int
    /// PR number, or `0` when there isn't one — holt doesn't null this
    /// field today (`internal/commands/json.go`'s `jsonPostMerge`), unlike
    /// `pr` at the envelope level. Treat `0` as "none" here, not as PR #0.
    public let pr: Int

    public init(commits: Int, pr: Int) {
        self.commits = commits
        self.pr = pr
    }
}

/// One lane, in the exact shape `--json` uses for `lanes[]` — the same
/// shape `watch --json` puts on `event.lane`. One schema whether you're
/// reading a snapshot or a stream (SPEC.md §14.1).
///
/// `occupied` and `dirty` are three-state on purpose: `nil` means "not
/// determined" (no lsof, no forge, cache miss), which is categorically
/// different from `false`. Every consumer bug in holt's bash-era statusline
/// came from collapsing that `nil` into `false` — do not do that here
/// either.
public struct HoltLane: Codable, Hashable, Sendable {
    public let name: String
    public let repo: String
    public let main: String
    public let branch: String
    public let path: String
    public let parent: String
    /// The client this lane opens (`claude` | `codex` | `opencode`, or
    /// whatever adapters are configured) — never the lane's own identity.
    public let agent: String
    public let state: LaneState
    public let occupied: Bool?
    public let dirty: Bool?
    public let landed: LandedInfo
    public let postMergeAhead: PostMergeAhead
    public let lastCommit: String

    enum CodingKeys: String, CodingKey {
        case name, repo, main, branch, path, parent, agent, state, occupied, dirty, landed
        case postMergeAhead = "post_merge_ahead"
        case lastCommit = "last_commit"
    }

    public init(
        name: String, repo: String, main: String, branch: String, path: String, parent: String,
        agent: String, state: LaneState, occupied: Bool?, dirty: Bool?, landed: LandedInfo,
        postMergeAhead: PostMergeAhead, lastCommit: String
    ) {
        self.name = name
        self.repo = repo
        self.main = main
        self.branch = branch
        self.path = path
        self.parent = parent
        self.agent = agent
        self.state = state
        self.occupied = occupied
        self.dirty = dirty
        self.landed = landed
        self.postMergeAhead = postMergeAhead
        self.lastCommit = lastCommit
    }
}

/// The `holt --json` / `holt list --json` envelope — byte-identical between
/// the two spellings (SPEC.md §2.2).
public struct HoltEnvelope: Codable, Hashable, Sendable {
    public let holt: String
    public let schema: Int
    public let lanes: [HoltLane]
    public let warnings: [String]

    public init(holt: String, schema: Int, lanes: [HoltLane], warnings: [String]) {
        self.holt = holt
        self.schema = schema
        self.lanes = lanes
        self.warnings = warnings
    }
}

// ---------------------------------------------------------------------------
// `holt watch --json` — SPEC.md §14.3 step 2, §14.4.

public enum WatchEventKindTag {}
/// Closed set, same discipline as `LaneState`/`LandedVerdict`: additions
/// are minor, removals major. An unrecognized kind is noise to ignore, not
/// an error to throw on — that's what lets holt add `landed`/
/// `source: "forge"` later without breaking every SDK pinned to v1
/// (SPEC.md §14.4).
public typealias WatchEventKind = HoltOpenEnum<WatchEventKindTag>
extension WatchEventKind {
    public static let sync = WatchEventKind(rawValue: "sync")
    public static let ready = WatchEventKind(rawValue: "ready")
    public static let created = WatchEventKind(rawValue: "created")
    public static let parked = WatchEventKind(rawValue: "parked")
    public static let resumed = WatchEventKind(rawValue: "resumed")
    public static let reaped = WatchEventKind(rawValue: "reaped")
    public static let changed = WatchEventKind(rawValue: "changed")
    public static let warning = WatchEventKind(rawValue: "warning")
}

/// First line of every `watch` stream. A version header, not an event —
/// see `capabilities` below for why it carries more than `{holt, schema}`.
public struct WatchHello: Codable, Hashable, Sendable {
    public let seq: Int
    public let holt: String
    public let schema: Int
    /// What families of event this holt build can ever send on this
    /// stream. v1 always sends exactly `["registry"]`; a future `"forge"`
    /// entry is how a consumer learns a `landed`/`post_merge_ahead` event
    /// kind might show up without guessing from which kinds happen to have
    /// arrived yet.
    public let capabilities: [String]

    enum CodingKeys: String, CodingKey {
        case seq, holt, schema, capabilities
    }
}

/// Every line after `hello`. One line, at most one lane — never a batch.
public struct WatchEvent: Codable, Hashable, Sendable {
    public let kind: WatchEventKind
    /// Monotonic across the WHOLE stream, hello included — lets a consumer
    /// fanning this out over its own transport (e.g. a websocket) detect a
    /// dropped line without holt knowing anything about that transport.
    public let seq: Int
    /// RFC3339 UTC, as sent on the wire. When THIS holt process observed
    /// the change, not necessarily when it happened at the source. `nil`
    /// on `hello` — never present here, since `WatchHello` is a separate
    /// type (see `WatchLine`).
    public let ts: String?
    /// Which provider produced the event. v1 only ever writes
    /// `"registry"`; absent on `ready`, which names no lane and no
    /// provider.
    public let source: String?
    /// Present on every kind except `ready` and `warning`.
    public let lane: HoltLane?
    /// Present only on `warning` — the same text `warnings[]` carries
    /// under `--json`, pushed here because a stream reader has no
    /// envelope to poll.
    public let message: String?
}

/// A line of `holt watch --json`: either the version header or a lifecycle
/// event. Swift has no anonymous union, so this stands in for the TS/
/// Python SDKs' `WatchHello | WatchEvent` — switch on it rather than
/// checking `.kind` by hand.
public enum WatchLine: Sendable {
    case hello(WatchHello)
    case event(WatchEvent)

    /// `"hello"` for the header, otherwise the event's own kind — matches
    /// `line.kind` in the TS/Python SDKs for parity, even though a Swift
    /// caller will usually just `switch` on the case instead.
    public var kind: String {
        switch self {
        case .hello: return "hello"
        case .event(let event): return event.kind.rawValue
        }
    }

    /// The lane an `.event` carries, or `nil` for `.hello` and for event
    /// kinds (`ready`, `warning`) that name no lane.
    public var lane: HoltLane? {
        switch self {
        case .hello: return nil
        case .event(let event): return event.lane
        }
    }
}

extension WatchLine: Decodable {
    private enum KindKey: String, CodingKey { case kind }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: KindKey.self)
        let kind = try container.decode(String.self, forKey: .kind)
        if kind == "hello" {
            self = .hello(try WatchHello(from: decoder))
        } else {
            self = .event(try WatchEvent(from: decoder))
        }
    }
}

/// `true` for the stream's version header. Named to match the TS/Python
/// SDKs' `isWatchHello`/`is_watch_hello`; a Swift caller will more often
/// just `switch` on `WatchLine` directly.
public func isWatchHello(_ line: WatchLine) -> Bool {
    if case .hello = line { return true }
    return false
}
