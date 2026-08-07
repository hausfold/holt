# holt (Rust SDK)

A thin Rust client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. holt stays a
binary; this crate shells out to it (`tokio::process` + `--json`, `watch
--json` for a live NDJSON stream) rather than talking to a daemon, because
there isn't one (SPEC.md §14.1).

Async (tokio), like the Python SDK's stance and for the same reason: `watch`
is naturally a stream, and the obvious host for this crate — a web backend
serving many concurrent agent sessions — is async-native in Rust too (axum,
tonic, actix). A sync caller can still drive every method with
`tokio::runtime::Runtime::block_on`.

Import name is `holt`; the crate on crates.io is `hausfold-holt` (`use
holt::HoltClient;` after adding `hausfold-holt` as a dependency) — the crate
name `holt` itself is already taken by an unrelated project, same split as
the PyPI package.

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
`HoltClient::default()` is a complete client; it's cheap to `clone()` and
safe to share across tasks, since every call is a fresh subprocess.

```rust
use futures_util::StreamExt;
use holt::HoltClient;

let client = HoltClient::default();

let envelope = client.list().await?;
for lane in &envelope.lanes {
    // occupied/dirty are `Option<bool>` — `None` means "not determined",
    // never coerce it to `false` (SPEC.md §2.2's whole nullable-
    // discipline point).
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
```

Dropping the `watch()`/`watch_lane()` stream kills the underlying `holt
watch` process (`kill_on_drop`) — there is no other way to stop it short:
`watch` has no built-in end condition, by design (SPEC.md §14).

**Interactive (a real terminal TUI).** `new_interactive` / `resume_interactive`
inherit the calling process's stdio, so when holt execs the configured
agent client (`claude`, `codex`, `opencode`), it takes over the real
terminal — same as running `holt new` by hand — and control returns to you
when that session ends.

```rust
// A terminal app, run in an actual TTY:
client.new_interactive(Some("task-42"), None).await?;
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `new_interactive` from a server.** `holt new` execs the agent
client unconditionally — it doesn't check for a TTY the way `resume` does —
so calling it with piped stdio blocks forever with your pipes attached to
whatever the agent expects on stdin. `resume()` (the non-interactive form)
is safe from a server: holt detects the piped stdout and prints the reopen
command as text instead of exec'ing.

## Holding a session open: leases, not callbacks

holt's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that. A server holding one session per lane has no
pane and no shell cwd'd anywhere — so it says so itself, with a lease:

```rust
let mut lease = client.lease(lane_dir, holt::LeaseOptions::default()); // refreshes on a background task, < the 90s TTL
// ... serve the session ...
lease.release().await?;
```

Pass `LeaseOptions { pid: Some(pid), ..Default::default() }` instead when the
lease should track a real local process — the OS then drops it the instant
that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there. See SPEC.md §14.2.

`HoltClient::lease` fires its first heartbeat on a background task rather
than blocking on it (the method isn't `async`, so it can't return an error
either) — a failed take surfaces on the next refresh, the same
best-effort/self-healing behavior every later refresh already has. Call
`client.heartbeat(...)` yourself first if you need the initial take to be
synchronous and its error immediate, same tradeoff as the Go SDK's `Lease`.
`Lease::release` needs `&mut self` and is idempotent; dropping a `Lease`
without calling `release()` stops the background refresh (so it doesn't
leak a task) but does **not** call `heartbeat --release` — the lease simply
stops renewing and expires on its own TTL, since `Drop` can't run async
code.

## Types for a frontend

`holt::types` (re-exported at the crate root) has no dependencies beyond
`serde` — `tokio::process` only appears in `exec`/`watch`/`lease`. Import
just the structs if you're modeling the same wire shape elsewhere (e.g. your
web backend fans `watch()` out over its own websocket, and something
downstream needs `Lane`/`WatchLine` to validate what it receives):

```rust
use holt::{Lane, WatchLine};
```

`lane_state`, `landed_verdict`, and `watch_kind` are plain `&str` constant
modules, not Rust `enum`s — `Lane::state`/`Landed::verdict`/`WatchLine::kind`
stay `String`. A real `enum` would either reject an unrecognized wire value
outright (a `Deserialize` error) or need a lossy catch-all variant that
throws away the original text on serialize; SPEC.md §2.2 requires the
opposite ("additions are minor, removals major" — a consumer must treat an
unknown value as opaque data, not an error), same discipline as the Go SDK's
`type LaneState string` with defined consts. Compare with `==` against the
constants (`lane.state == holt::lane_state::LIVE`).

## What's NOT here yet

- `hook create`/`hook remove` (the Claude Code hook protocol, SPEC.md §2.3)
  have no wrapper — they're for editor integrations, not the orchestrator
  use case this SDK targets first. Shell out via `tokio::process::Command`
  yourself if you need them.
- The `--json` envelope's future fields (`pr`, `overlap`, `ahead`/`behind` —
  SPEC.md §2.2's example, gated behind the `overlap`/forge-polling
  milestones) aren't in `Lane` because they aren't on the wire in schema 1
  yet. Don't add them here before `internal/commands/json.go` does.
- Types are hand-ported from the Go structs, not generated, same as the
  TS/Python/Swift/Go SDKs. If holt's JSON shape and this file drift, that's
  a real bug class this SDK exists to avoid — SPEC.md §14.1 says "generate
  SDK types from it" as the intended end state.
- **crates.io publish.** The crate name (`hausfold-holt`) is picked and
  confirmed free, but nothing is published yet — for now, reference it from
  within this repo or a `path`/`git` dependency. Needs a crates.io API token
  under the `hausfold` account/org before `cargo publish` can run (see the
  repo README's SDKs section for the equivalent npm/PyPI gaps).

## Testing

`tests/fake-holt.sh` stands in for the real binary so tests don't need a Go
build of `holt` itself — it's a fixture, not a spec of holt's behavior, kept
in sync by hand with `sdk/ts`, `sdk/python`, `sdk/swift` and `sdk/go`'s
fixture of the same name. Once `holt` builds in CI, add a second suite that
runs the same assertions against the real binary in a scratch repo.

```sh
cargo test
cargo clippy --all-targets -- -D warnings
```
