from __future__ import annotations

from .types import HoltExitCode

_LABELS: dict[int, str] = {
    HoltExitCode.USAGE: "usage",
    HoltExitCode.REFUSED: "refused",
    HoltExitCode.DEGRADED: "degraded",
    HoltExitCode.CONFLICT: "conflict",
    HoltExitCode.LOCKED: "locked",
}


class HoltError(Exception):
    """Raised by every SDK call that shells out and gets back a non-zero
    exit. Carries holt's actual exit code (SPEC.md §2.4) rather than
    collapsing it to a generic failure — `err.refused` is how a caller tells
    "holt declined to destroy something" from "you asked wrong" (`USAGE`) or
    "registry locked" (`LOCKED`), and each deserves different handling
    (retry, surface to a human, or just don't retry).
    """

    def __init__(self, code: int, stderr: str, command: list[str]) -> None:
        label = _LABELS.get(code, f"exit {code}")
        suffix = f" — {stderr.strip()}" if stderr else ""
        super().__init__(f"holt {' '.join(command)}: {label}{suffix}")
        self.code = code
        self.stderr = stderr
        self.command = command

    @property
    def refused(self) -> bool:
        """`True` when holt declined for safety (occupied, dirty, or not
        provably landed) rather than because the call itself was wrong."""
        return self.code == HoltExitCode.REFUSED

    @property
    def degraded(self) -> bool:
        """`True` when the operation completed but a signal was unavailable
        (forge down, no `lsof`) — check `warnings` on the envelope for
        why."""
        return self.code == HoltExitCode.DEGRADED
