# 🦦 holt

**The worktree-lifecycle substrate for parallel coding agents.**
*Never loses work. Never reaps what's in use. The registry — not the filesystem — is truth.*

Every vendor now ships worktree *creation* — `claude --worktree`, the Claude
Agent SDK's `isolation: worktree`, Cursor, Copilot CLI — and every one of
them stops there. Nobody owns the rest of the life: **the branch that's
still alive after the pane died**, **the checkout nobody is sitting in
anymore**, **the tree with 40 uncommitted minutes in it**, **the branch
whose PR merged yesterday and has kept committing since**. If you've ever
`git worktree list`'d your way through a graveyard trying to remember which
of these you can safely delete — *that's* the problem holt makes go away.

## Quickstart

```bash
# spin up a lane on this repo: new checkout, new branch, agent opens in it
holt new fix-flaky-test

# see every lane you've got going, live or parked, across every repo
holt

# pane's closing and the tree's dirty? don't reach for `git stash` — it's
# shared across every worktree of this repo. `park` commits it instead, as
# a wip: commit only this branch has
holt park "mid-refactor on the retry logic"

# back later — rebuild the checkout, reopen the agent, right where you left it
holt fix-flaky-test

# sweep every lane whose branch already landed and nobody's standing in
holt reap

# working on a DIFFERENT repo than this pane — never a raw `git worktree add`
cd "$(holt child ../other-repo)"
```

## What's in a lane?

A **lane** is one agent's branch, checkout, and pane — not a "worktree" (a
parked lane has no checkout on disk, only a branch), and not a "session"
(that's your multiplexer's). `holt --json` returns lanes in this shape; the
SDKs below wrap it.

| Field | Meaning |
|---|---|
| `name` | lane name |
| `repo` / `main` | repo identity, and the main checkout's path |
| `branch` | full branch name |
| `path` | checkout path on disk — empty once `parked` |
| `parent` | the pane that spawned it via `holt child`, or `""` |
| `agent` | `claude` \| `codex` \| `opencode` |
| `state` | `live` \| `parked` \| `stray` |
| `occupied` | is anything actually standing in it right now |
| `dirty` | uncommitted changes |
| `landed` | reached the default branch? `yes` \| `no` \| `contained` |
| `post_merge_ahead` | commits made *after* the PR merged |
| `last_commit` | most recent commit |

Full lifecycle (states, invariants, landing rules, policy hooks) →
[`docs/lifecycle.md`](docs/lifecycle.md).

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

Config, exit codes, and building from source →
[`docs/reference.md`](docs/reference.md).

---

*Why not your agent's built-in worktrees? No vendor ships cross-client
(`claude` **and** `codex` **and** `opencode`, one registry), none ship
cross-repo, and losing your work isn't their problem — it's holt's.*

## SDKs

<details>
<summary><strong>TypeScript</strong> — <code>@hausfold/holt</code> (not published yet)</summary>

[`sdk/ts`](sdk/ts) — `list()`, `watch()` as an async iterator over the
NDJSON stream, `child`/`spawn` to create a lane without attaching an agent,
`park`/`unpark`/`reap`/`reship`, and occupancy leases. Works from a
Bun/Node TUI or a web backend; its types are safe to import into a browser
bundle for the frontend.

```ts
import { HoltClient } from "@hausfold/holt";

const holt = new HoltClient();
const envelope = await holt.list();
for await (const line of holt.watch()) {
  if (line.kind === "created") console.log("new lane:", line.lane?.name);
}
```

See [`sdk/ts/README.md`](sdk/ts/README.md) for the full API.

</details>

<details>
<summary><strong>Python</strong> — <code>hausfold-holt</code></summary>

[`sdk/python`](sdk/python) — the same client, async-first
(`asyncio.create_subprocess_exec`): `list()`, `watch()` as an async
iterator, `child`/`spawn`, `park`/`unpark`/`reap`/`reship`, and occupancy
leases. Drops into a FastAPI/asyncio backend or a plain script equally.

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

[`sdk/swift`](sdk/swift) — the same client over `Foundation.Process`:
`list()`, `watch()`/`watchLane(path:)` as `AsyncThrowingStream`,
`child`/`spawn`, `park`/`unpark`/`reap`/`reship`, and occupancy leases.
macOS + Linux — not iOS/tvOS/watchOS, since `Process` can't spawn a
subprocess there.

Ships from a generated mirror,
[`nebelhaus/holt-swift`](https://github.com/nebelhaus/holt-swift) — send
changes to `sdk/swift` here, never to the mirror directly.

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

[`sdk/go`](sdk/go) — the same client over `os/exec`: `List`,
`Watch`/`WatchLane` as Go 1.23 range-over-func iterators, `Child`/`Spawn`,
`Park`/`Unpark`/`Reap`/`Reship`, and occupancy leases via `Client.Lease`.

The one SDK with zero setup: its own nested `go.mod`
(`github.com/nebelhaus/holt/sdk/go`) means `go get` resolves it straight
from this repo — no publish step, no separate mirror. No tagged release
yet, so `go get github.com/nebelhaus/holt/sdk/go@<commit-sha>` for now.

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

<details>
<summary><strong>Rust</strong> — <code>hausfold-holt</code></summary>

[`sdk/rust`](sdk/rust) — the same client, async (tokio): `list()`,
`watch()`/`watch_lane()` as a `Stream` of typed lines, `child`/`spawn`,
`park`/`unpark`/`reap`/`reship`, and occupancy leases via `HoltClient::lease`.
Drops into an axum/tonic backend or a plain async binary equally.

```sh
cargo add hausfold-holt
```

```rust
use futures_util::StreamExt;
use holt::HoltClient;

let client = HoltClient::default();
let envelope = client.list().await?;
let mut lines = Box::pin(client.watch());
while let Some(line) = lines.next().await {
    if line?.kind == holt::watch_kind::CREATED {
        println!("new lane created");
    }
}
```

See [`sdk/rust/README.md`](sdk/rust/README.md) for the full API.

</details>

---

<p align="center"><a href="https://hausfold.co">⌂ hausfold</a></p>
