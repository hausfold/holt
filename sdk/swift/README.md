# Holt (Swift SDK)

A thin Swift client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. holt stays a
binary; this SDK shells out to it (`Process` + `--json`, `watch --json`
for a live NDJSON stream) rather than talking to a daemon, because there
isn't one (SPEC.md §14.1).

macOS only, no iOS/tvOS/watchOS: `Foundation.Process` cannot spawn a
subprocess on those platforms (App Store sandboxing forbids it outright,
and there's no `holt` binary to bundle even if it could). Linux works too
— `Process` is part of `swift-corelibs-foundation` — for the server /
long-running-orchestrator case SPEC.md §14 calls out as the first real
consumer; SwiftPM's `platforms:` field just doesn't list it because that
field only constrains Apple OSes. Needs Swift 5.9+ (uses
`FileHandle.bytes.lines` and `AsyncThrowingStream`).

## Install

Not published yet — for now, reference it as a local package
(`.package(path: "../holt/sdk/swift")`) or copy `sdk/swift` out. Swift
Package Manager has no monorepo-subdirectory story for a *remote* git
dependency the way `npm`/`pip` do (no `#subdirectory=` equivalent) — see
"What's NOT here yet" below for what publishing this actually needs.

`holt` itself must be on `PATH`, or pass `HoltClientOptions(bin: "/path/to/holt")`.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `HoltClient`
method except the two ending in `Interactive` captures the child's stdout
and returns — safe to call from a server with many concurrent sessions.
`HoltClient` is a `Sendable` value type; construct one per call if you like.

```swift
import Holt

let holt = HoltClient()

let envelope = try await holt.list()
for lane in envelope.lanes {
    // occupied/dirty are `Bool?` — nil means "not determined", never
    // coerce it to false (SPEC.md §2.2's whole nullable-discipline point).
    print(lane.name, lane.state, lane.occupied as Any)
}

// Create a lane WITHOUT attaching an agent to it — the primitive an
// orchestrator wants. `child`/`spawn` only ever print the new path.
let dir = try await holt.child("/path/to/some-repo", name: "task-42")
// ...now launch YOUR OWN agent process against `dir`.
```

```swift
// Live updates instead of polling — created/parked/resumed/reaped/changed.
for try await line in holt.watch() {
    if case .event(let event) = line, event.kind == .created {
        notifyUI(event.lane)
    }
}
```

**Interactive (a real terminal app).** `newInteractive` /
`resumeInteractive` inherit the calling process's stdio, so when holt
execs the configured agent client (`claude`, `codex`, `opencode`), it
takes over the real terminal — same as running `holt new` by hand — and
control returns to you when that session ends.

```swift
// Run in an actual terminal:
try await holt.newInteractive("task-42")
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `newInteractive` from a server.** `holt new` execs the
agent client unconditionally — it doesn't check for a TTY the way
`resume` does — so calling it with piped stdio blocks forever with your
pipes attached to whatever the agent expects on stdin. `resume()` (the
non-interactive form) is safe from a server: holt detects the piped
stdout and prints the reopen command as text instead of exec'ing.

## Holding a session open: leases, not callbacks

holt's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that. A server holding one session per lane has
no pane and no shell cwd'd anywhere — so it says so itself, with a lease:

```swift
let lease = try await holt.lease(path: laneDir) // refreshes on an interval, < the 90s TTL
// ... serve the session ...
await lease.release()
```

Pass `pid:` instead when the lease should track a real local process —
the OS then drops it the instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one —
"nobody leased it" isn't proof nobody's there. See SPEC.md §14.2.

`holt.lease(...)` is a throwing `async` factory here (an `actor Lease`
comes back once the first heartbeat has succeeded), the same tradeoff the
Python SDK made and unlike the TS SDK's constructor-based `lease()`:
Swift can await that first heartbeat before returning, so a failure to
take the lease raises immediately instead of surfacing on the next
refresh or release call.

## `watch()` cleanup

`watch()`/`watchLane(path:)` return an `AsyncThrowingStream`. The
underlying process is killed via the stream's `onTermination` handler,
which fires when you `break` out of a `for try await` loop or the
enclosing `Task` is cancelled — there is no other way to stop it short:
`watch` has no built-in end condition, by design (SPEC.md §14).

## Types for a UI layer

`Types.swift` has no dependencies beyond `Foundation`'s `Codable`
machinery — `Process`/`Pipe`/`FileHandle` only appear in `Exec.swift`/
`Watch.swift`/`Client.swift`. Import just the types if something else in
your app needs to model the same wire shape (e.g. a SwiftUI view model
fed by your own actor wrapping `watch()`):

```swift
import Holt // HoltLane, WatchEvent, …
```

`LaneState`, `LandedVerdict`, `LandedVia`, and `WatchEventKind` are not
Swift `enum`s — they're `HoltOpenEnum<Tag>`, a `RawRepresentable` string
wrapper with `static let` cases. A real `enum: String, Codable` throws a
`DecodingError` on an unrecognized case; SPEC.md §2.2 requires the
opposite ("additions are minor, removals major" — a consumer must treat
an unknown value as opaque, not an error), same as the TS/Python SDKs'
plain string-literal/`Literal` types, which perform no runtime check at
all. Compare with `==` and the `static let` constants (`lane.state ==
.live`); switching exhaustively isn't available and isn't the point.

## What's NOT here yet

- `hook create`/`hook remove` (the Claude Code hook protocol, SPEC.md
  §2.3) have no wrapper — they're for editor integrations, not the
  orchestrator use case this SDK targets first. Shell out via the free
  `run()` function if you need them.
- The `--json` envelope's future fields (`pr`, `overlap`, `ahead`/
  `behind` — SPEC.md §2.2's example, gated behind the `overlap`/forge-
  polling milestones) aren't in `HoltLane` because they aren't on the
  wire in schema 1 yet. Don't add them here before
  `internal/commands/json.go` does.
- Types are hand-ported from the Go structs, not generated, same as the
  TS/Python SDKs. If holt's JSON shape and this file drift, that's a real
  bug class this SDK exists to avoid — SPEC.md §14.1 says "generate SDK
  types from it" as the intended end state.
- **Real publishing.** This lives at `sdk/swift` inside the `holt` repo,
  same holding pattern as `sdk/ts`/`sdk/python` before they published.
  Swift Package Manager has no equivalent of `npm`'s scoped package or
  `pip`'s `#subdirectory=` for a monorepo — a *remote* git dependency
  needs `Package.swift` at the repo root. Getting this to a real
  `.package(url: "https://github.com/nebelhaus/holt-swift", from: "0.1.0")`
  needs either (a) a standalone mirror repo (e.g. `nebelhaus/holt-swift`)
  that this directory is synced or subtree-split into, or (b) tagging
  releases straight off this repo and accepting that consumers pin a
  `branch`/`revision` instead of a semver range pointed at a subdirectory
  — SwiftPM does not support the latter for the root manifest location.
  Neither needs a package-manager *account*: unlike npm/PyPI, SwiftPM has
  no central index to publish to for git-URL consumption. Optional next
  steps, once there's a tagged release: listing on the (free, no-account)
  [Swift Package Index](https://swiftpackageindex.com) via a PR to their
  repo, and/or publishing to a Swift Package **Registry** (SE-0292) if
  one is wanted — that does need an account on whichever registry you
  pick (GitHub's supports it under a repo's own auth; there's no single
  "the" Swift registry the way PyPI/npmjs.org are canonical).

## Testing

`Tests/HoltTests/fake-holt.sh` stands in for the real binary so tests
don't need a Go build — it's a fixture, not a spec of holt's behavior,
kept in sync by hand with `sdk/ts/test/fake-holt.sh` /
`sdk/python/tests/fake-holt.sh`. Once `holt` builds in CI, add a second
suite that runs the same assertions against the real binary in a scratch
repo.

```
swift test
swift build
```
