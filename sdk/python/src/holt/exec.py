from __future__ import annotations

import asyncio
import json
import os
from dataclasses import dataclass
from typing import Any, Optional

from .errors import HoltError


@dataclass
class RunOptions:
    """Options threaded through to every subprocess call."""

    # Path to the holt binary, or a bare name resolved on PATH. Defaults to
    # "holt".
    bin: Optional[str] = None
    # Working directory to run holt from — most commands are cwd-sensitive
    # (`holt new`, `holt park`, a bare `holt <name>`).
    cwd: Optional[str] = None
    # Extra environment variables, merged over the current process's env —
    # e.g. HOLT_AGENT, HOLT_OCCUPANCY. A value of None unsets the key rather
    # than passing the literal string "None" to the child.
    env: Optional[dict[str, Optional[str]]] = None
    # Piped to the child's stdin, then the stream is closed. Used by
    # `holt hook create`/`remove`, which read JSON off stdin (SPEC.md §2.3).
    stdin: Optional[str] = None


@dataclass
class RunResult:
    stdout: str
    stderr: str
    code: int


def merged_env(env: Optional[dict[str, Optional[str]]]) -> Optional[dict[str, str]]:
    if env is None:
        return None
    merged = dict(os.environ)
    for key, value in env.items():
        if value is None:
            merged.pop(key, None)
        else:
            merged[key] = value
    return merged


async def run(args: list[str], opts: Optional[RunOptions] = None) -> RunResult:
    """Runs one holt invocation to completion and collects its output. Every
    non-`--json` holt command writes human text to stdout on success — this
    is the primitive `list()`/`watch()` build their typed parsing on top of,
    and the one lifecycle commands (`new`, `park`, `reap`, ...) use directly,
    surfacing stdout as a plain string.

    Raises {HoltError} on a non-zero exit, carrying holt's exit code
    (SPEC.md §2.4) rather than collapsing every failure into one shape.
    """
    opts = opts or RunOptions()
    bin_ = opts.bin or "holt"
    proc = await asyncio.create_subprocess_exec(
        bin_,
        *args,
        cwd=opts.cwd,
        env=merged_env(opts.env),
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdin_bytes = opts.stdin.encode() if opts.stdin is not None else b""
    stdout_bytes, stderr_bytes = await proc.communicate(stdin_bytes)
    stdout = stdout_bytes.decode()
    stderr = stderr_bytes.decode()
    code = proc.returncode if proc.returncode is not None else 1

    if code != 0:
        raise HoltError(code, stderr, [bin_, *args])
    return RunResult(stdout=stdout, stderr=stderr, code=code)


async def run_json(args: list[str], opts: Optional[RunOptions] = None) -> Any:
    """Same as {run}, but parses stdout as JSON — for `--json` commands
    only. holt's own contract (README, internal/ui) is "stdout carries the
    payload, every diagnostic goes to stderr", so this never has to guess
    which lines are data."""
    result = await run(args, opts)
    return json.loads(result.stdout)
