# @hausfold/holt (TypeScript SDK)

A thin TypeScript client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. It shells out to
`holt` (`exec` + `--json`, `watch --json` for a live NDJSON stream) rather
than talking to a daemon.

Works from Bun or Node ≥18. Import just `types` for a browser bundle.

## Install

```sh
bun add @hausfold/holt
# or: npm install @hausfold/holt
```

`holt` itself must be on `PATH`, or pass `{ bin: "/path/to/holt" }`.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `HoltClient`
method except the two ending in `Interactive` captures the child's stdout
and returns — safe to call from a server with many concurrent sessions.

```ts
import { HoltClient } from "@hausfold/holt";

const holt = new HoltClient();

const envelope = await holt.list();
for (const lane of envelope.lanes) {
  // occupied/dirty are `boolean | null` — null means "not determined",
  // never coerce it to false.
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

// Or scoped to the one lane this session holds — no hello/ready framing,
// and nothing about anybody else's lanes.
for await (const event of holt.watchLane(dir)) {
  if (event.kind === "reaped") endSession();
}
```

**Interactive (a real terminal TUI).** `newInteractive` / `resumeInteractive`
inherit the calling process's stdio, so holt execs the configured agent
client (`claude`, `codex`, `opencode`, `pi`) and takes over the real terminal —
control returns to you when that session ends.

```ts
// Bun/Node TUI, run in an actual terminal:
await holt.newInteractive("task-42");
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `newInteractive` from a server** — it execs the agent client
unconditionally, without checking for a TTY, so piped stdio blocks forever.
Use `resume()` instead: it detects piped stdout and prints the reopen
command as text rather than exec'ing.

## Holding a session open: leases

holt's sweep (`reap`) needs to know a checkout is in use. A human's `lsof`
answers that; a server holding one session per lane has no pane or shell
to check, so it says so itself with a lease:

```ts
const lease = holt.lease(laneDir); // refreshes on an interval, < the 90s TTL
// ... serve the session ...
await lease.release();
```

Pass `{ pid }` instead to track a real local process — the OS drops the
lease the instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there.

## Types for a frontend

`src/types.ts` has no runtime dependencies — Node builtins only appear in
`exec.ts`/`watch.ts`/`client.ts`. Import just the types into a browser
bundle:

```ts
import type { HoltLane, WatchEvent } from "@hausfold/holt";
```

## What's NOT here yet

`hook create`/`hook remove` have no wrapper — shell out via `run()` if you
need them.

## Testing

`test/fake-holt.sh` is a fixture standing in for the real binary.

```
bun test
bun run typecheck
bun run build
```
