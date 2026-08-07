# hausfold-holt (Python SDK)

A thin Python client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. holt stays a
binary; this SDK shells out to it (`asyncio.create_subprocess_exec` +
`--json`, `watch --json` for a live NDJSON stream) rather than talking to a
daemon, because there isn't one (SPEC.md §14.1).

Async-first, like the wire protocol wants: `watch()` is naturally a stream,
and the obvious host for this SDK — a web backend serving many concurrent
agent sessions — is async-native in Python too (FastAPI, Starlette, aiohttp).
A sync script can still call every method with `asyncio.run(...)`.

Import name is `holt`; the package on PyPI is `hausfold-holt` (`import
holt` after `pip install hausfold-holt`, same split as most `<org>-<name>`
distributions).

## Install

Not published yet — for now, reference it from within this repo
(`pip install -e sdk/python`) or copy `sdk/python` out. Once published:
`pip install hausfold-holt` / `uv add hausfold-holt`.

`holt` itself must be on `PATH`, or pass `HoltClientOptions(bin="/path/to/holt")`.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `HoltClient`
method except the two ending in `_interactive` captures the child's stdout
and returns — safe to call from a server with many concurrent sessions.

```python
import asyncio
from holt import HoltClient

async def main() -> None:
    holt = HoltClient()

    envelope = await holt.list()
    for lane in envelope.lanes:
        # occupied/dirty are `bool | None` — None means "not determined",
        # never coerce it to False (SPEC.md §2.2's whole nullable-discipline
        # point).
        print(lane.name, lane.state, lane.occupied)

    # Create a lane WITHOUT attaching an agent to it — the primitive an
    # orchestrator wants. child/spawn only ever print the new path.
    lane_dir = await holt.child("/path/to/some-repo", "task-42")
    # ...now launch YOUR OWN agent process against lane_dir.

asyncio.run(main())
```

```python
# Live updates instead of polling — created/parked/resumed/reaped/changed.
async for line in holt.watch():
    if line.kind == "created" and line.lane is not None:
        notify_ui(line.lane)
```

**Interactive (a real terminal TUI).** `new_interactive` /
`resume_interactive` inherit the calling process's stdio, so when holt
execs the configured agent client (`claude`, `codex`, `opencode`), it takes
over the real terminal — same as running `holt new` by hand — and control
returns to you when that session ends.

```python
# A terminal app, run in an actual TTY:
await holt.new_interactive("task-42")
# ... the agent owned the screen; you're back here when it exits.
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

```python
lease = await holt.lease(lane_dir)  # refreshes on an interval, < the 90s TTL
# ... serve the session ...
await lease.release()
```

Pass `pid=` instead when the lease should track a real local process — the
OS then drops it the instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there. See SPEC.md §14.2.

`holt.lease(...)` is a coroutine here, unlike the TS SDK's constructor-based
`holt.lease(...)`: Python can await the first heartbeat before returning, so
a failure to take the lease raises immediately instead of surfacing on the
next refresh or release call.

## `watch()` cleanup

`watch()` returns an async generator; stop consuming (`break`, or
`.aclose()`) to kill the underlying process. CPython's refcounting closes a
*sync* generator promptly when it goes out of scope, but async generators
aren't guaranteed the same — wrap long-lived use in `contextlib.aclosing()`
if you want the subprocess torn down deterministically rather than on the
next GC pass:

```python
from contextlib import aclosing

async with aclosing(holt.watch()) as stream:
    async for line in stream:
        ...
```

## Types for a frontend

`holt.types` has no runtime dependencies beyond the standard library —
`subprocess`/`asyncio` only appear in `exec.py`/`watch.py`/`client.py`.
Import just the dataclasses if you're modeling the same wire shape
elsewhere (e.g. your web backend fans `watch()` out over its own
websocket, and something downstream needs `HoltLane`/`WatchEvent` to
validate what it receives):

```python
from holt import HoltLane, WatchEvent
```

## What's NOT here yet

- `hook create`/`hook remove` (the Claude Code hook protocol, SPEC.md §2.3)
  have no wrapper — they're for editor integrations, not the orchestrator
  use case this SDK targets first. Shell out via `run()` if you need them.
- The `--json` envelope's future fields (`pr`, `overlap`, `ahead`/`behind` —
  SPEC.md §2.2's example, gated behind the `overlap`/forge-polling
  milestones) aren't in `HoltLane` because they aren't on the wire in
  schema 1 yet. Don't add them here before `internal/commands/json.go` does.
- Types are hand-ported from the Go structs, not generated, same as the TS
  SDK. If holt's JSON shape and this file drift, that's a real bug class
  this SDK exists to avoid — SPEC.md §14.1 says "generate SDK types from
  it" as the intended end state.

## Testing

`tests/fake-holt.sh` stands in for the real binary so tests don't need a Go
build — it's a fixture, not a spec of holt's behavior, kept in sync by hand
with `sdk/ts/test/fake-holt.sh`. Once `holt` builds in CI, add a second
suite that runs the same assertions against the real binary in a scratch
repo.

```
python -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
pytest
mypy src
```
