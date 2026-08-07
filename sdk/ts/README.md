# holt-sdk (TypeScript)

A thin TypeScript client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. holt stays a
binary; this SDK shells out to it (`exec` + `--json`, `watch --json` for a
live NDJSON stream) rather than talking to a daemon, because there isn't
one (SPEC.md §14.1).

Works from Bun or Node ≥18. Nothing in `src/` uses a Bun-only API, so the
same build runs a TUI's local process and a web server's backend; import
just the `types` for a browser bundle.

## Install

Not published yet — for now, reference it from within this repo or copy
`sdk/ts` out. Once published: `bun add holt-sdk` / `npm i holt-sdk`.

`holt` itself must be on `PATH`, or pass `{ bin: "/path/to/holt" }`.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `HoltClient`
method except the two ending in `Interactive` captures the child's stdout
and returns — safe to call from a server with many concurrent sessions.

```ts
import { HoltClient } from "holt-sdk";

const holt = new HoltClient();

const envelope = await holt.list();
for (const lane of envelope.lanes) {
  // occupied/dirty are `boolean | null` — null means "not determined",
  // never coerce it to false (SPEC.md §2.2's whole nullable-discipline point).
  console.log(lane.name, lane.state, lane.occupied);
}

// Create a lane WITHOUT attaching an agent to it — the primitive an
// orchestrator wants. `child`/`spawn` only ever print the new path.
const dir = await holt.child("/path/to/some-repo", "task-42");
// ...now launch YOUR OWN agent process against `dir`.
```

```ts
// Live updates instead of polling — created/parked/resumed/reaped/changed.
for await (const line of holt.watch()) {
  if (line.kind === "created") notifyUI(line.lane);
}
```

**Interactive (a real terminal TUI).** `newInteractive` / `resumeInteractive`
inherit the calling process's stdio, so when holt execs the configured
agent client (`claude`, `codex`, `opencode`), it takes over the real
terminal — same as running `holt new` by hand — and control returns to you
when that session ends.

```ts
// Bun/Node TUI, run in an actual terminal:
await holt.newInteractive("task-42");
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `newInteractive` from a server.** `holt new` execs the agent
client unconditionally — it doesn't check for a TTY the way `resume` does —
so calling it with piped stdio blocks forever with your pipes attached to
whatever the agent expects on stdin. `resume()` (the non-interactive form)
is safe from a server: holt detects the piped stdout and prints the reopen
command as text instead of exec'ing.

## Holding a session open: leases, not callbacks

holt's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that. A server holding one session per lane has no
pane and no shell cwd'd anywhere — so it says so itself, with a lease:

```ts
const lease = holt.lease(laneDir); // refreshes on an interval, < the 90s TTL
// ... serve the session ...
await lease.release();
```

Pass `{ pid }` instead when the lease should track a real local process —
the OS then drops it the instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there. See SPEC.md §14.2.

## Types for a frontend

`src/types.ts` has no runtime dependencies — Node builtins only appear in
`exec.ts`/`watch.ts`/`client.ts`. Import just the types into a browser
bundle (e.g. your web backend fans `watch()` out over its own websocket,
and the frontend needs `WatchEvent`/`HoltLane` to type the messages it
receives):

```ts
import type { HoltLane, WatchEvent } from "holt-sdk";
```

## What's NOT here yet

- `hook create`/`hook remove` (the Claude Code hook protocol, SPEC.md §2.3)
  have no wrapper — they're for editor integrations, not the orchestrator
  use case this SDK targets first. Shell out via `run()` if you need them.
- The `--json` envelope's future fields (`pr`, `overlap`, `ahead`/`behind` —
  SPEC.md §2.2's example, gated behind the `overlap`/forge-polling
  milestones) aren't in `HoltLane` because they aren't on the wire in
  schema 1 yet. Don't add them here before `internal/commands/json.go` does.
- Types are hand-ported from the Go structs, not generated. If holt's JSON
  shape and this file drift, that's a real bug class this SDK exists to
  avoid — SPEC.md §14.1 says "generate SDK types from it" as the intended
  end state. A `go generate` step emitting these `.ts` files is the
  natural fix; out of scope for this first pass.

## Testing

`test/fake-holt.sh` stands in for the real binary so tests don't need a Go
build — it's a fixture, not a spec of holt's behavior. Once `holt` builds
in CI, add a second suite that runs the same assertions against the real
binary in a scratch repo.

```
bun test
bun run typecheck
bun run build
```
