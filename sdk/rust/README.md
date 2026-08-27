# holt (Rust SDK)

A thin Rust client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. It shells out to
`holt` (`tokio::process` + `--json`, `watch --json` for a live NDJSON
stream) rather than talking to a daemon.

Async (tokio): `watch()` is naturally a stream. A sync caller can still
drive every method with `tokio::runtime::Runtime::block_on`.

Import name is `holt`; the crate on crates.io is `hausfold-holt` (plain
`holt` is already taken by an unrelated project).

## Install

```sh
cargo add hausfold-holt
```

```toml
[dependencies]
hausfold-holt = "0.1"
```

For local development against this repo instead, point at the path:

```toml
[dependencies]
hausfold-holt = { path = "../holt/sdk/rust", package = "hausfold-holt" }
```

`holt` itself must be on `PATH`, or set `HoltClient { bin: Some("/path/to/holt".into()), ..Default::default() }`.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `HoltClient` method
except the two ending in `_interactive` captures the child's stdout and
returns — safe to call from a server with many concurrent sessions.
`HoltClient::default()` is a complete client; cheap to `clone()` and safe to
share across tasks, since every call is a fresh subprocess.

```rust
use futures_util::StreamExt;
use holt::HoltClient;

let client = HoltClient::default();

let envelope = client.list().await?;
for lane in &envelope.lanes {
    // occupied/dirty are `Option<bool>` — `None` means "not determined",
    // never coerce it to `false`.
    println!("{} {} {:?}", lane.name, lane.state, lane.occupied);
}

// Create a lane WITHOUT attaching an agent to it — the primitive an
// orchestrator wants. child/spawn only ever print the new path.
let dir = client.child("/path/to/some-repo", Some("task-42")).await?;
// ...now launch YOUR OWN agent process against `dir`.

// Live updates instead of polling — created/parked/resumed/reaped/changed.
let mut lines = Box::pin(client.watch());
while let Some(line) = lines.next().await {
    let line = line?;
    if line.kind == holt::watch_kind::CREATED {
        notify_ui(line.lane);
    }
}

// Scoped to one lane: same stream, minus the hello/ready framing that
// names no lane. Its item is `WatchEvent`, not `WatchLine` — hello is
// already filtered out, so the header-only fields (`holt`, `schema`,
// `capabilities`) aren't in the type at all. Same contract as `watchLane`
// in the TS/Python/Swift SDKs.
let mut events = Box::pin(client.watch_lane(&dir));
while let Some(event) = events.next().await {
    let event = event?;
    // `sync` arrives here too, and is NOT framing: it's how a caller that
    // started watching late learns the lane exists at all.
    render(&event.kind, event.lane);
}
```

Consuming `watch()` and want the same narrower type? `line.into_event()`
returns `None` for the hello header and `Some(WatchEvent)` for everything
else.

Dropping the `watch()`/`watch_lane()` stream kills the underlying `holt
watch` process (`kill_on_drop`) — that's the only way to stop it, since
`watch` has no built-in end condition.

**Interactive (a real terminal TUI).** `new_interactive` / `resume_interactive`
inherit the calling process's stdio, so when holt execs the configured
agent client (`claude`, `codex`, `opencode`, `pi`), it takes over the real
terminal — same as running `holt new` by hand — and control returns to you
when that session ends.

```rust
// A terminal app, run in an actual TTY:
client.new_interactive(Some("task-42"), None).await?;
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `new_interactive` from a server** — it execs the agent client
unconditionally, without checking for a TTY, so piped stdio blocks forever.
Use `resume()` instead: it detects piped stdout and prints the reopen
command as text rather than exec'ing.

## Holding a session open: leases

holt's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that; a server holding one session per lane has no
pane or shell to check, so it says so itself with a lease:

```rust
let mut lease = client.lease(lane_dir, holt::LeaseOptions::default()); // refreshes on a background task, < the 90s TTL
// ... serve the session ...
lease.release().await?;
```

Pass `LeaseOptions { pid: Some(pid), ..Default::default() }` instead when
the lease should track a real local process — the OS then drops it the
instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there.

`HoltClient::lease` fires its first heartbeat on a background task rather
than blocking on it, so a failed take surfaces on the next refresh rather
than immediately. Call `client.heartbeat(...)` yourself first if you need
the initial take to be synchronous and its error immediate. `Lease::release`
needs `&mut self` and is idempotent; dropping a `Lease` without calling
`release()` stops the background refresh but does **not** release the
lease server-side — it simply stops renewing and expires on its own TTL,
since `Drop` can't run async code.

## Types for a frontend

`holt::types` (re-exported at the crate root) has no dependencies beyond
`serde`. Import just the structs if you're modeling the same wire shape
elsewhere:

```rust
use holt::{Lane, WatchEvent, WatchLine};
```

`lane_state`, `landed_verdict`, and `watch_kind` are plain `&str` constant
modules, not Rust `enum`s — `Lane::state`/`Landed::verdict`/`WatchLine::kind`
stay `String`, so an unrecognized wire value decodes as opaque data instead
of failing. Compare with `==` against the constants
(`lane.state == holt::lane_state::LIVE`).

## What's NOT here yet

`hook create`/`hook remove` have no wrapper — shell out via
`tokio::process::Command` yourself if you need them. Types are hand-ported
from the Go structs, not generated; if holt's JSON shape drifts from this
file, that's a bug here.

## Testing

`tests/fake-holt.sh` stands in for the real binary, shared with the other
SDKs' fixtures of the same name.

```sh
cargo test
cargo clippy --all-targets -- -D warnings
```
