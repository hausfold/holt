# hausfold-scruff (Python SDK)

A thin Python client over the [`scruff`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. scruff has no daemon,
so this SDK shells out to it (`asyncio.create_subprocess_exec` + `--json`,
`watch --json` for a live NDJSON stream).

Async-first: `watch()` is naturally a stream. A sync script can still call
every method via `asyncio.run(...)`.

Import name is `scruff`; the package on PyPI is `hausfold-scruff`.

## Install

```
pip install hausfold-scruff
# or: uv add hausfold-scruff
```

For local development against this repo instead: `pip install -e sdk/python`.

`scruff` itself must be on `PATH`, or pass `ScruffClientOptions(bin="/path/to/scruff")`.

## Two shapes of usage

**Programmatic.** Every `ScruffClient` method except the two ending in
`_interactive` captures the child's stdout and returns — safe to call from
a server with many concurrent sessions.

```python
import asyncio
from scruff import ScruffClient

async def main() -> None:
    scruff = ScruffClient()

    envelope = await scruff.list()
    for lane in envelope.lanes:
        # occupied/dirty are `bool | None`: None means "not determined",
        # never coerce it to False.
        print(lane.name, lane.state, lane.occupied)

    # Create a lane WITHOUT attaching an agent to it — the primitive an
    # orchestrator wants. child/spawn only ever print the new path.
    lane_dir = await scruff.child("/path/to/some-repo", "task-42")
    # ...now launch YOUR OWN agent process against lane_dir.

asyncio.run(main())
```

```python
# Live updates instead of polling — created/parked/resumed/reaped/changed.
async for line in scruff.watch():
    if line.kind == "created" and line.lane is not None:
        notify_ui(line.lane)

# Or scoped to the one lane this session holds — no hello/ready framing,
# and nothing about anybody else's lanes.
async for event in scruff.watch_lane(lane_dir):
    if event.kind == "reaped":
        end_session()
```

**Interactive.** `new_interactive` / `resume_interactive` inherit the
calling process's stdio, so when scruff execs the configured agent client
(`claude`, `codex`, `opencode`, `pi`) it takes over the real terminal — same as
running `scruff new` by hand — and control returns to you when that session
ends.

```python
# A terminal app, run in an actual TTY:
await scruff.new_interactive("task-42")
# ... the agent owned the screen; you're back here when it exits.
```

**Do not call `new_interactive` from a server** — `scruff new` execs the
agent client unconditionally, without checking for a TTY, so piped stdio
blocks forever. Use `resume()` instead: it detects piped stdout and prints
the reopen command as text rather than exec'ing.

## Holding a session open: leases

scruff's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that; a server has no pane or shell cwd'd anywhere,
so it says so itself with a lease:

```python
lease = await scruff.lease(lane_dir)  # refreshes on an interval, < the 90s TTL
# ... serve the session ...
await lease.release()
```

Pass `pid=` instead when the lease should track a real local process — the
OS then drops it the instant that pid dies, no refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one — "nobody
leased it" isn't proof nobody's there.

`scruff.lease(...)` is a coroutine, so it can await the first heartbeat before
returning: a failure to take the lease raises immediately instead of
surfacing on the next refresh or release call.

## `watch()` cleanup

`watch()` returns an async generator; stop consuming (`break`, or
`.aclose()`) to kill the underlying process. Async generators aren't
guaranteed to close promptly when they go out of scope — wrap long-lived use
in `contextlib.aclosing()` to tear down the subprocess deterministically:

```python
from contextlib import aclosing

async with aclosing(scruff.watch()) as stream:
    async for line in stream:
        ...
```

## Types for a frontend

`scruff.types` has no runtime dependencies beyond the standard library.
Import just the dataclasses if you're modeling the same wire shape
elsewhere:

```python
from scruff import ScruffLane, WatchEvent
```

## What's NOT here yet

`hook create`/`hook remove` have no wrapper — shell out via `run()` if you
need them. Types are hand-ported from the Go structs, not generated; if
scruff's JSON shape drifts from this file, that's a bug here.

## Testing

`tests/fake-scruff.sh` stands in for the real binary so tests don't need a Go
build.

```
python -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
pytest
mypy src
```
