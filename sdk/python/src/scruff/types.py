# Wire types for scruff's frozen public contracts — SPEC.md §2.2 (`--json`) and
# §14.3 step 2 (`watch --json`). Hand-ported from the Go source of truth
# (internal/commands/json.go, internal/commands/watch.go) rather than
# generated, because scruff has no `go generate` step yet for this — see the
# SDK README for the drift risk that implies.
#
# schema 1 is what's actually implemented today. SPEC.md §2.2's example
# envelope also shows `pr`, `overlap`, `ahead`, `behind` — those are §0.2/
# later milestones (`overlap`, forge polling) and are NOT on the wire yet.
# Do not add them here until json.go does; a field that exists in the type
# but never arrives on the wire is worse than one that's simply missing.

from __future__ import annotations

from dataclasses import dataclass
from enum import IntEnum
from typing import Any, Literal, Optional, Union


class ScruffExitCode(IntEnum):
    """scruff's exit-code contract (SPEC.md §2.4). REFUSED vs USAGE is the one
    that matters to a caller: "you asked wrong" vs "I declined to destroy
    something"."""

    OK = 0
    USAGE = 1
    REFUSED = 2
    DEGRADED = 3
    CONFLICT = 4
    LOCKED = 5


# Closed set — SPEC.md §2.2: "additions are minor, removals major." Treat an
# unknown value as opaque, not an error.
LaneState = Literal["live", "parked", "stray"]

LandedVerdict = Literal["yes", "no", "fresh", "contained"]

LandedVia = Optional[
    Literal[
        "never-diverged",
        "ancestry",
        "pr-head-oid",
        "patch-equivalence",
        "merge-tree-empty",
    ]
]


@dataclass(frozen=True, slots=True)
class LandedInfo:
    verdict: LandedVerdict
    via: LandedVia
    confidence: str

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> "LandedInfo":
        return cls(verdict=d["verdict"], via=d.get("via"), confidence=d["confidence"])


@dataclass(frozen=True, slots=True)
class PostMergeAhead:
    commits: int
    # PR number, or 0 when there isn't one — scruff doesn't null this field
    # today (internal/commands/json.go's jsonPostMerge), unlike `pr` at the
    # envelope level. Treat 0 as "none" here, not as PR #0.
    pr: int

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> "PostMergeAhead":
        return cls(commits=d["commits"], pr=d["pr"])


@dataclass(frozen=True, slots=True)
class ScruffLane:
    """One lane, in the exact shape `--json` uses for `lanes[]` — the same
    shape `watch --json` puts on `event.lane`. One schema whether you're
    reading a snapshot or a stream (SPEC.md §14.1).

    `occupied` and `dirty` are three-state on purpose: `None` means "not
    determined" (no lsof, no forge, cache miss), which is categorically
    different from `False`. Every consumer bug in scruff's bash-era statusline
    came from collapsing that `None` into `False` — do not do that here
    either.
    """

    name: str
    repo: str
    main: str
    branch: str
    path: str
    parent: str
    # The client this lane opens (claude | codex | opencode | pi, or whatever
    # adapters are configured) — never the lane's own identity.
    agent: str
    state: LaneState
    occupied: Optional[bool]
    dirty: Optional[bool]
    landed: LandedInfo
    post_merge_ahead: PostMergeAhead
    last_commit: str

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> "ScruffLane":
        return cls(
            name=d["name"],
            repo=d["repo"],
            main=d["main"],
            branch=d["branch"],
            path=d["path"],
            parent=d["parent"],
            agent=d["agent"],
            state=d["state"],
            occupied=d["occupied"],
            dirty=d["dirty"],
            landed=LandedInfo._from_json(d["landed"]),
            post_merge_ahead=PostMergeAhead._from_json(d["post_merge_ahead"]),
            last_commit=d["last_commit"],
        )


@dataclass(frozen=True, slots=True)
class ScruffEnvelope:
    """The `scruff --json` / `scruff list --json` envelope — byte-identical
    between the two spellings (SPEC.md §2.2)."""

    scruff: str
    schema: int
    lanes: list[ScruffLane]
    warnings: list[str]

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> "ScruffEnvelope":
        return cls(
            scruff=d["scruff"],
            schema=d["schema"],
            lanes=[ScruffLane._from_json(lane) for lane in d["lanes"]],
            warnings=list(d["warnings"]),
        )


# ---------------------------------------------------------------------------
# `scruff watch --json` — SPEC.md §14.3 step 2, §14.4.

# Closed set, same discipline as LaneState/LandedVerdict: additions are
# minor, removals major. An unknown kind is noise to ignore, not an error to
# raise on — that's what lets scruff add `landed`/`source: "forge"` later
# without breaking every SDK pinned to v1 (SPEC.md §14.4).
WatchEventKind = Literal[
    "sync", "ready", "created", "parked", "resumed", "reaped", "changed", "warning"
]


@dataclass(frozen=True, slots=True)
class WatchHello:
    """First line of every `watch` stream. A version header, not an event —
    see `capabilities` for why it carries more than `{scruff, schema}`."""

    kind: Literal["hello"]
    seq: int
    scruff: str
    schema: int
    # What families of event this scruff build can ever send on this stream.
    # v1 always sends exactly ["registry"]; a future "forge" entry is how a
    # consumer learns a landed/post_merge_ahead event kind might show up
    # without guessing from which kinds happen to have arrived yet.
    capabilities: list[str]


@dataclass(frozen=True, slots=True)
class WatchEvent:
    """Every line after `hello`. One line, at most one lane — never a
    batch."""

    kind: WatchEventKind
    # Monotonic across the WHOLE stream, hello included — lets a consumer
    # fanning this out over its own transport (e.g. a websocket) detect a
    # dropped line without scruff knowing anything about that transport.
    seq: int
    # RFC3339 UTC. When THIS scruff process observed the change, not
    # necessarily when it happened at the source. Absent on `hello`.
    ts: Optional[str] = None
    # Which provider produced the event. v1 only ever writes "registry";
    # absent on `ready`, which names no lane and no provider.
    source: Optional[str] = None
    # Present on every kind except `ready` and `warning`.
    lane: Optional[ScruffLane] = None
    # Present only on `warning` — the same text `warnings[]` carries under
    # `--json`, pushed here because a stream reader has no envelope to poll.
    message: Optional[str] = None


WatchLine = Union[WatchHello, WatchEvent]


def parse_watch_line(d: dict[str, Any]) -> WatchLine:
    if d["kind"] == "hello":
        return WatchHello(
            kind="hello",
            seq=d["seq"],
            scruff=d["scruff"],
            schema=d["schema"],
            capabilities=list(d["capabilities"]),
        )
    return WatchEvent(
        kind=d["kind"],
        seq=d["seq"],
        ts=d.get("ts"),
        source=d.get("source"),
        lane=ScruffLane._from_json(d["lane"]) if d.get("lane") is not None else None,
        message=d.get("message"),
    )


def is_watch_hello(line: WatchLine) -> bool:
    return isinstance(line, WatchHello)
