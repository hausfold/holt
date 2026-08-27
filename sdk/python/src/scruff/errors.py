from __future__ import annotations

from .types import ScruffExitCode

_LABELS: dict[int, str] = {
    ScruffExitCode.USAGE: "usage",
    ScruffExitCode.REFUSED: "refused",
    ScruffExitCode.DEGRADED: "degraded",
    ScruffExitCode.CONFLICT: "conflict",
    ScruffExitCode.LOCKED: "locked",
}


class ScruffError(Exception):
    """Raised by every SDK call that shells out and gets back a non-zero
    exit. Carries scruff's actual exit code (SPEC.md §2.4) rather than
    collapsing it to a generic failure — `err.refused` is how a caller tells
    "scruff declined to destroy something" from "you asked wrong" (`USAGE`) or
    "registry locked" (`LOCKED`), and each deserves different handling
    (retry, surface to a human, or just don't retry).
    """

    def __init__(self, code: int, stderr: str, command: list[str]) -> None:
        label = _LABELS.get(code, f"exit {code}")
        suffix = f" — {stderr.strip()}" if stderr else ""
        super().__init__(f"scruff {' '.join(command)}: {label}{suffix}")
        self.code = code
        self.stderr = stderr
        self.command = command

    @property
    def refused(self) -> bool:
        """`True` when scruff declined for safety (occupied, dirty, or not
        provably landed) rather than because the call itself was wrong."""
        return self.code == ScruffExitCode.REFUSED

    @property
    def degraded(self) -> bool:
        """`True` when the operation completed but a signal was unavailable
        (forge down, no `lsof`) — check `warnings` on the envelope for
        why."""
        return self.code == ScruffExitCode.DEGRADED
