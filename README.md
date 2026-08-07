# 🦦 holt

**The worktree-lifecycle substrate for parallel coding agents.**

*(a holt is an otter's den — the thing that keeps several of them from being
underfoot in one burrow)*

Every vendor now ships worktree *creation* — `claude --worktree`, the Claude
Agent SDK's `isolation: worktree`, Cursor, Copilot CLI — and every one of them
stops there. Nobody owns the rest of the life: the branch that's still alive
after the pane died, the checkout nobody is sitting in anymore, the tree with
40 uncommitted minutes in it, the branch whose PR merged yesterday and has
kept committing since. If you've ever `git worktree list`'d your way through
a graveyard trying to remember which of these you can safely delete — that's
the problem holt exists to make go away.

```
create ──▶ live ──▶ parked ──▶ live ──▶ landed ──▶ reaped
             │        │                    ▲
             └────────┴────────────────────┘
```

The thing moving through those states is a **lane**: one agent's branch,
checkout and pane. Not a "worktree" — a parked lane has no checkout on disk
at all, and the branch is what survives. Not an "agent" either: that word is
reserved for the *client* a lane runs (`claude`, `codex`, `opencode`), and
not "session", which belongs to your multiplexer and to the clients' own
transcripts.

Three invariants, in priority order — they are the product:

1. **Never lose work.** Every destructive path parks first. The failure
   direction is always "a branch lingers", never "a tree vanished".
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed
   means keep. Uncertainty resolves to *keep*, including when the forge is
   unreachable.
3. **The registry is the source of truth, and it is locked.** Not the
   filesystem, not `git worktree list` — those are derived, and they lie.

What you *do* at each transition — build, test, deploy — is yours. holt has
no opinion about your build system, and none about which agent you run.

## Why not just use your agent's built-in worktrees

Because a vendor will never ship cross-client (nobody is going to support
`codex` **and** `opencode` **and** `claude` in one registry), never ship
cross-repo parentage, and treats the lifecycle invariants as an afterthought
— because losing *your* work isn't *their* problem.

## Quickstart

```bash
# spin up a lane on this repo: new checkout, new branch, agent opens in it
holt new fix-flaky-test

# see every lane you've got going, live or parked, across every repo — not
# just this one
holt

# pane's closing and the tree's dirty? don't reach for `git stash` — its
# stack is shared across every worktree of this repo, so another lane's
# `pop` can hand your uncommitted work to an agent that never asked for it.
# `park` commits it instead, as a wip: commit only this branch has
holt park "mid-refactor on the retry logic"

# back later — rebuild the checkout, reopen the agent, exactly where you left it
holt fix-flaky-test

# a lane whose branch merged three lanes ago is still sitting on disk —
# sweep every LANDED lane that nobody is standing in
holt reap

# working on a DIFFERENT repo than the one this pane is in (a parent repo
# editing a vendored sub-repo, say) — never a raw `git worktree add`, or the
# child becomes invisible to anything watching the registry
cd "$(holt child ../other-repo)"
```

## What's in a lane?

Every lane is one row in the registry, plus a little derived state computed
live off git and the forge — this is the shape `holt --json` hands you and
what the SDKs below wrap:

| Field | Meaning |
|---|---|
| `name` | lane name — the branch minus its `worktree-` prefix |
| `repo` / `main` | the repo's identity, and the path of its main checkout |
| `branch` | the full branch name |
| `path` | checkout path on disk — empty/absent once the lane is `parked` |
| `parent` | cwd of the pane that spawned it via `holt child`, or `""` |
| `agent` | the client this lane runs: `claude` \| `codex` \| `opencode` |
| `state` | `live` (checkout resolves), `parked` (branch only), or `stray` (an orphaned directory git has disowned) |
| `occupied` | is something — a pane, a lease — actually standing in it right now |
| `dirty` | uncommitted changes on disk |
| `landed` | has this branch's work reached the default branch — `yes` \| `no` \| `contained`, with `via` and a `confidence` |
| `post_merge_ahead` | commits made *after* this branch's PR merged — the "kept committing after landing" case, with a commit count and PR number |
| `last_commit` | most recent commit on the branch |

## Commands

```
holt                    list every live/parked lane, across all repos
holt <name>             resume one: rebuild its checkout, reopen its agent
holt new [name]         a lane on THIS repo, then open the default agent in it
holt child <repo>       a lane on ANOTHER repo, as a child of this pane
holt spawn <repo> <name>
                        a named lane for a spawner with no pane of its own
holt park [label]       set the working tree aside as a wip: commit on this branch
holt unpark             put the last parked commit's changes back, uncommitted
holt reap               sweep every LANDED lane that nobody is standing in
holt heartbeat [path]   hold the occupancy lease on a lane, so reap spares it
holt watch --json       lifecycle events on stdout, one NDJSON object per line
holt reship [name]      push a branch that outran its merged PR, open the follow-up
holt hook create        [hook] open a lane — JSON on stdin, path on stdout
holt hook remove        [hook] retire one without losing work — JSON on stdin
```

`holt heartbeat` is how anything that isn't a human at a terminal — a
container, a CI runner, an orchestrator embedding holt — tells `reap` it's
using a checkout. A lease naming a live pid self-expires the moment that
process dies; use `--pid 0` and refresh within 90s when there's no local
process to watch. A lease can only ever *save* a lane from the sweep, never
condemn one — "nobody leased it" isn't proof nobody's there.

`holt watch --json` streams lifecycle events (`created`, `parked`, `resumed`,
`reaped`, `changed`) as NDJSON, one object per line, for anything embedding
holt — an agent UI, a server holding one session per lane. Design rationale
in [`SPEC.md` §14](SPEC.md); the wire format is `internal/commands/watch.go`'s
doc comment and the SDKs' `watch()` return types.

### SDKs

<details>
<summary><strong>TypeScript</strong> — <code>@hausfold/holt</code> (not published yet)</summary>

[`sdk/ts`](sdk/ts) is a thin client over the binary — `list()`, `watch()` as
an async iterator over the NDJSON stream, `child`/`spawn` to create a lane
without attaching an agent, `park`/`unpark`/`reap`/`reship`, and occupancy
leases. Works from a Bun/Node TUI or a web backend; its types are safe to
import into a browser bundle for the frontend.

The npm package name is decided (`@hausfold/holt`) but nothing is published
yet — for now, reference it from within this repo or copy `sdk/ts` out. This
section moves to a proper docs page once it ships.

```ts
import { HoltClient } from "@hausfold/holt";

const holt = new HoltClient();
const envelope = await holt.list();
for await (const line of holt.watch()) {
  if (line.kind === "created") console.log("new lane:", line.lane?.name);
}
```

See [`sdk/ts/README.md`](sdk/ts/README.md) for the full API, the
programmatic-vs-interactive split (`new`/`resume` vs `newInteractive`/
`resumeInteractive`), and leases.

</details>

<details>
<summary><strong>Python</strong> — <code>hausfold-holt</code></summary>

[`sdk/python`](sdk/python) is the same thin client, async-first
(`asyncio.create_subprocess_exec`): `list()`, `watch()` as an async
iterator, `child`/`spawn`, `park`/`unpark`/`reap`/`reship`, and occupancy
leases (a throwing `async` factory here, unlike the TS SDK's constructor-
based one). Drops into a FastAPI/asyncio backend or a plain script equally.

```
pip install hausfold-holt
# or: uv add hausfold-holt
```

```python
import asyncio
from holt import HoltClient

async def main() -> None:
    holt = HoltClient()
    envelope = await holt.list()
    async for line in holt.watch():
        if line.kind == "created" and line.lane is not None:
            print("new lane:", line.lane.name)

asyncio.run(main())
```

See [`sdk/python/README.md`](sdk/python/README.md) for the full API.

</details>

<details>
<summary><strong>Swift</strong> — <code>Holt</code></summary>

[`sdk/swift`](sdk/swift) is the same thin client over
`Foundation.Process`: `list()`, `watch()`/`watchLane(path:)` as
`AsyncThrowingStream`, `child`/`spawn`, `park`/`unpark`/`reap`/`reship`,
and occupancy leases (an `actor Lease`, taken via a throwing `async`
factory). macOS + Linux — not iOS/tvOS/watchOS, since `Process` can't
spawn a subprocess there.

Swift Package Manager has no monorepo-subdirectory story for a remote git
dependency, so this ships from a generated mirror,
[`nebelhaus/holt-swift`](https://github.com/nebelhaus/holt-swift)
(`git subtree split --prefix=sdk/swift`, tagged to match) — send changes
to `sdk/swift` here, never to the mirror directly.

```swift
.package(url: "https://github.com/nebelhaus/holt-swift", from: "0.1.0")
```

```swift
import Holt

let holt = HoltClient()
let envelope = try await holt.list()
for try await line in holt.watch() {
    if case .event(let event) = line, event.kind == .created {
        print("new lane:", event.lane?.name ?? "?")
    }
}
```

See [`sdk/swift/README.md`](sdk/swift/README.md) for the full API.

</details>

<details>
<summary><strong>Go</strong> — <code>github.com/nebelhaus/holt/sdk/go</code></summary>

[`sdk/go`](sdk/go) is the same thin client over `os/exec`: `List`,
`Watch`/`WatchLane` as Go 1.23 range-over-func iterators (`iter.Seq2`, no
channel or goroutine bridging needed — the generator body runs
synchronously on the caller's own loop), `Child`/`Spawn`, `Park`/
`Unpark`/`Reap`/`Reship`, and occupancy leases (`*Lease`, taken via
`Client.Lease`, refreshed on a background goroutine until `Release`).
Every method takes a `context.Context` first, Go's own idiom for what the
other SDKs do with generator `.return()`/`break`.

It's the one SDK that installs with zero setup: `sdk/go` carries its own
nested `go.mod` (`github.com/nebelhaus/holt/sdk/go`), so `go get` resolves
it straight from this repo via the module proxy — no npm/PyPI-style
publish step, no package-manager account, and no separate mirror repo
(unlike Swift's SwiftPM problem above). A pushed `sdk/go/vX.Y.Z` tag is
all a real release needs; none exist yet, so `go get
github.com/nebelhaus/holt/sdk/go@<commit-sha>` for now.

```go
import (
	"context"
	holt "github.com/nebelhaus/holt/sdk/go"
)

c := &holt.Client{}
envelope, err := c.List(context.Background())
for line, err := range c.Watch(context.Background()) {
	if err == nil && line.Kind == holt.WatchCreated {
		fmt.Println("new lane:", line.Lane.Name)
	}
}
```

See [`sdk/go/README.md`](sdk/go/README.md) for the full API.

</details>

## Default agent

Set a durable default that works from Zellij, launchd, and a standalone terminal:

```toml
# ~/.config/holt/config.toml
agent = "codex"
```

`HOLT_AGENT` overrides that file for one invocation.

## Disagreeing with holt

holt grew out of one machine's setup, and it inherited that machine's answers
to questions that only *look* universal — what "landed" means, whether a
closing pane's dirty tree becomes a `wip:` commit, how "resume" reopens a
session. Each of those is a named **seam**: a program you can drop in that
holt execs, handing it the situation as JSON on stdin, reading the answer off
the exit code (`0` yes, `1` no, `2` refused, `3` "no opinion, use the
built-in"). A broken or missing hook always falls back to the built-in —
loudly — so overriding one costs you the override at worst, never the safety
net.

```toml
# ~/.config/holt/config.toml
[hooks]
resume = "/usr/local/bin/my-resume"   # e.g. open a pane instead of taking over this process
landed = ["/usr/local/bin/my-landed", "--release-train"]
```

Two things no seam can override: the checkout holt is being **run from** is
never swept, and a **stray** directory is only ever reported, never deleted.

Full protocol, the shipped seams, and what "landed" handles (fast-forward,
merge commit, forge rebase, squash, cherry-pick, merged-then-kept-committing)
in [`SPEC.md` §6.5](SPEC.md) and [§3](SPEC.md).

## Exit codes

| | |
|---|---|
| 0 | success, including "nothing to do" |
| 1 | usage / precondition error |
| 2 | **refused for safety** — occupied, dirty, or not provably landed |
| 3 | degraded — completed, but a signal was unavailable |
| 4 | conflict found (a finding, not an error) |
| 5 | registry locked by another holt |

`2` vs `1` is the one that matters: a wrapper script must be able to tell
"you asked wrong" from "I declined to destroy something".

## Building

```bash
make check
```

or `nix develop` for a shell with Go, bats and `gh`.

## License

Apache-2.0.

---

Part of the [hausfold](https://hausfold.co) toolkit, alongside nebelhaus,
pounce, perch, and nebelung.
