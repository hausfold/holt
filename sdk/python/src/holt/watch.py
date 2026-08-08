from __future__ import annotations

import asyncio
import json
from typing import AsyncGenerator, Optional

from .exec import RunOptions, merged_env
from .types import WatchEvent, WatchLine, parse_watch_line


async def watch_all(opts: Optional[RunOptions] = None) -> AsyncGenerator[WatchLine, None]:
    """`holt watch --json` as an async iterator of typed lines. One object
    per NDJSON line on stdout, in order: `hello`, a `sync` burst for every
    lane already alive, `ready`, then live changes for as long as the
    process runs (SPEC.md §14.3 step 2).

    The child process is killed when you stop consuming — `break` out of an
    `async for`, or call `.aclose()` on the generator. Async generators are
    not guaranteed to be finalized promptly by garbage collection the way
    CPython's refcounting closes sync generators, so wrap long-lived use in
    `contextlib.aclosing()` if you want the subprocess torn down
    deterministically rather than on the next GC pass:

    ```python
    from contextlib import aclosing

    async with aclosing(holt.watch()) as stream:
        async for line in stream:
            ...
    ```

    There is no other way to stop it short: `watch` has no built-in end
    condition, by design (SPEC.md §14).
    """
    opts = opts or RunOptions()
    bin_ = opts.bin or "holt"
    proc = await asyncio.create_subprocess_exec(
        bin_,
        "watch",
        "--json",
        cwd=opts.cwd,
        env=merged_env(opts.env),
        stdin=asyncio.subprocess.DEVNULL,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    assert proc.stdout is not None
    try:
        while True:
            raw = await proc.stdout.readline()
            if not raw:
                break
            line = raw.decode().strip()
            if not line:
                continue
            yield parse_watch_line(json.loads(line))
    finally:
        if proc.returncode is None:
            proc.kill()
            await proc.wait()


async def watch_lane(path: str, opts: Optional[RunOptions] = None) -> AsyncGenerator[WatchEvent, None]:
    """{watch_all}, filtered to events about one lane (`event.lane.path`)
    and stripped of the `hello`/`ready` framing that names no lane — the
    shape an embedder holding one session per lane usually wants: "tell me
    when THIS lane's state changes."

    A `sync` event for the lane still passes through — it is NOT framing.
    It's how a caller that started watching after the lane went live learns
    the lane exists at all, so a match over `event.kind` needs a `sync` arm.

    Compare full paths, not names: names aren't unique across repos, but a
    checkout path is the registry's own primary key (SPEC.md §2.1).
    """
    async for line in watch_all(opts):
        if isinstance(line, WatchEvent) and line.lane is not None and line.lane.path == path:
            yield line
