from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any, AsyncGenerator, Optional

from .errors import HoltError
from .exec import RunOptions, merged_env, run, run_json
from .types import HoltEnvelope, WatchLine
from .watch import watch_all


@dataclass
class HoltClientOptions:
    # Path to the holt binary, or a bare name resolved on PATH. Defaults to
    # "holt".
    bin: Optional[str] = None
    # Working directory every command runs from — most of holt's commands
    # are cwd-sensitive (`new`, `park`, a bare `holt <name>`). Defaults to
    # this process's own cwd.
    cwd: Optional[str] = None
    # Extra environment variables, merged over the current process's env.
    # Useful for HOLT_AGENT, HOLT_OCCUPANCY=lease.
    env: Optional[dict[str, Optional[str]]] = None


class HoltClient:
    """A thin client over the `holt` binary. Every method shells out —
    there is no daemon, no port, no socket (SPEC.md §14.1) — so this class
    holds nothing but the options each call needs, and is cheap to
    construct as often as you like.

    Two methods (`new_interactive`, `resume_interactive`) inherit the
    calling process's stdio and can hand off the terminal to a coding
    agent; every other method captures output and returns. Mixing them up
    matters: see each method's docstring.
    """

    def __init__(self, options: Optional[HoltClientOptions] = None) -> None:
        options = options or HoltClientOptions()
        self._opts = RunOptions(bin=options.bin, cwd=options.cwd, env=options.env)

    async def list(self) -> HoltEnvelope:
        """`holt --json` / `holt list --json` — byte-identical (SPEC.md
        §2.2). The full snapshot: every live/parked lane, across every repo
        holt knows about. Poll this for landedness and PR state; use
        {watch} for everything else, since it's push rather than poll."""
        data = await run_json(["--json"], self._opts)
        return HoltEnvelope._from_json(data)

    def watch(self) -> AsyncGenerator[WatchLine, None]:
        """`holt watch --json` as an async iterator of typed lines — a
        `hello`, then a `sync` burst for every lane already alive, `ready`,
        then live changes for as long as you keep iterating. Stop
        iterating (`break`, or `.aclose()`) to kill the underlying
        process.

        This is the primitive onOpen/onParked/... callback-style APIs are
        built from (SPEC.md §14.2) — see `holt.watch.watch_lane` for a
        version scoped to one lane's `path`.

        ```python
        async for line in holt.watch():
            if line.kind == "created":
                print("new lane:", line.lane.name if line.lane else None)
        ```
        """
        return watch_all(self._opts)

    async def child(self, repo_path: str, name: Optional[str] = None) -> str:
        """`holt child <repo> [name]` — a lane on ANOTHER repo, registered
        as a child of cwd. Prints only the new checkout's path on stdout
        (SPEC.md §2.3's "only the path" discipline extends here too) and
        never execs a client, which is what makes it the right primitive
        for an orchestrator: create the lane, then run your OWN agent
        process against the path it returns."""
        args = ["child", repo_path, *([name] if name else [])]
        result = await run(args, self._opts)
        return result.stdout.strip()

    async def spawn(self, repo_path: str, name: str, agent: Optional[str] = None) -> str:
        """`holt spawn <repo> <name> [agent]` — a named lane for a caller
        with no pane of its own (a scheduler, a web backend). Like
        {child}, only ever creates the lane and prints its path; never
        execs."""
        args = ["spawn", repo_path, name, *([agent] if agent else [])]
        result = await run(args, self._opts)
        return result.stdout.strip()

    async def resume(self, name: str) -> str:
        """`holt <name>` / `holt resume <name>` with stdout captured
        rather than a terminal — which means the Go binary's own TTY check
        (`ui.IsTTY`) sees a pipe and, by design, never execs a client. It
        rebuilds the checkout if needed and returns the human-readable
        result: either confirmation it's ready, or the exact command to
        reopen the agent's chat by hand. Safe to call from a server
        process. For a TUI that wants to actually hand off the terminal,
        use {resume_interactive} instead."""
        result = await run(["resume", name], self._opts)
        return result.stdout

    async def park(self, label: Optional[str] = None) -> None:
        """`holt park [label]` — commits the working tree as one `wip:`
        commit on the current branch. Never touches the shared stash stack
        (README's "park, not git stash" section) — this is the one safe
        way for concurrent lanes to set work aside."""
        await run(["park", *([label] if label else [])], self._opts)

    async def unpark(self) -> None:
        """`holt unpark` — reverses the most recent `park`, putting its
        changes back uncommitted. Raises {HoltError} with `.refused ==
        True` if that commit is already pushed (holt will not rewrite
        published history) or HEAD isn't a parked commit."""
        await run(["unpark"], self._opts)

    async def reap(self) -> None:
        """`holt reap` — sweeps every LANDED lane nobody is standing in
        (occupied, per {heartbeat}/`lsof`, always wins). Never removes the
        checkout holt is being run from, and never removes a stray."""
        await run(["reap"], self._opts)

    async def reship(self, name: Optional[str] = None) -> None:
        """`holt reship [name]` — pushes a branch that outran its already-
        merged PR, and opens the follow-up. Raises with `.degraded ==
        True` if `gh` itself is unavailable."""
        await run(["reship", *([name] if name else [])], self._opts)

    async def heartbeat(self, path: Optional[str] = None, *, pid: Optional[int] = None) -> None:
        """`holt heartbeat [path] [--pid N | --release]` — takes or
        refreshes the occupancy lease on a checkout (SPEC.md §9.1, §14.2).
        This is the seam built for exactly this SDK: a program embedding
        holt has no pane and no shell cwd'd anywhere, so the lease is the
        only way `reap` learns a checkout is in use. A lease can only SAVE
        a lane from the sweep, never condemn one — see {lease} for a
        self-refreshing wrapper instead of calling this on a timer
        yourself."""
        args = ["heartbeat", *([path] if path else [])]
        if pid is not None:
            args += ["--pid", str(pid)]
        await run(args, self._opts)

    async def release_heartbeat(self, path: Optional[str] = None) -> None:
        """Drops the lease taken by {heartbeat}."""
        await run(["heartbeat", *([path] if path else []), "--release"], self._opts)

    async def lease(
        self,
        path: str,
        *,
        pid: Optional[int] = None,
        refresh_seconds: float = 60.0,
    ) -> "Lease":
        """Takes an occupancy lease and holds it for as long as the
        returned handle is open, refreshing on an interval comfortably
        under the 90s TTL (`internal/occupancy.TTL`) that applies when
        there's no pid to watch. This is the primitive an embedder's
        "session" (a connection, not a cwd — SPEC.md §14.2) should hold
        from connect to disconnect:

        ```python
        lease = await holt.lease(lane_dir)
        # ... serve the session ...
        await lease.release()
        ```

        Pass `pid=` instead when the lease should track a real local
        process — the kernel then releases it the instant that pid dies,
        with no refresh loop needed at all, and `refresh_seconds` is
        ignored.

        Unlike the TS SDK's constructor-based `lease()`, this is a
        coroutine: Python can await the first heartbeat before returning,
        so a failure to take the lease raises here rather than silently
        surfacing on the next refresh or release.
        """
        kwargs: dict[str, Any] = {"pid": pid} if pid is not None else {}
        await self.heartbeat(path, **kwargs)
        return Lease(self, path, pid=pid, refresh_seconds=refresh_seconds)

    async def new_interactive(self, name: Optional[str] = None, agent: Optional[str] = None) -> None:
        """`holt new [name] [agent]` with stdio INHERITED from the calling
        process. holt execs the configured agent client unconditionally
        here (unlike `resume`, `new` doesn't check for a TTY) —
        appropriate for a real terminal app (a TUI) that wants to hand off
        the screen and get control back when the agent session ends, and
        WRONG for a server: it will block until the agent process exits,
        with your stdio attached to whatever the agent expects."""
        args = ["new", *([name] if name else []), *([agent] if agent else [])]
        await _run_interactive(args, self._opts)

    async def resume_interactive(self, name: str) -> None:
        """`holt resume <name>` / `holt <name>` with stdio INHERITED, so a
        real terminal's TTY check passes and holt hands off the screen to
        the agent client. Same caveat as {new_interactive}: blocks until
        that session ends."""
        await _run_interactive(["resume", name], self._opts)


async def _run_interactive(args: list[str], opts: RunOptions) -> None:
    bin_ = opts.bin or "holt"
    proc = await asyncio.create_subprocess_exec(
        bin_,
        *args,
        cwd=opts.cwd,
        env=merged_env(opts.env),
        # stdin/stdout/stderr default to None, i.e. inherited from this
        # process — the async equivalent of Node's stdio: "inherit".
    )
    code = await proc.wait()
    if code != 0:
        raise HoltError(code, "", [bin_, *args])


class Lease:
    """A held occupancy lease. See {HoltClient.lease}."""

    def __init__(
        self,
        client: HoltClient,
        path: str,
        *,
        pid: Optional[int],
        refresh_seconds: float,
    ) -> None:
        self._client = client
        self._path = path
        self._released = False
        self._task: Optional["asyncio.Task[None]"] = None
        if pid is None:
            self._task = asyncio.create_task(self._refresh_loop(refresh_seconds))

    async def _refresh_loop(self, refresh_seconds: float) -> None:
        try:
            while True:
                await asyncio.sleep(refresh_seconds)
                try:
                    await self._client.heartbeat(self._path)
                except HoltError:
                    pass  # best-effort refresh; a miss self-heals on the next tick
        except asyncio.CancelledError:
            pass

    async def release(self) -> None:
        """Drops the lease and stops refreshing it. Safe to call more than
        once."""
        if self._released:
            return
        self._released = True
        if self._task is not None:
            self._task.cancel()
        await self._client.release_heartbeat(self._path)
