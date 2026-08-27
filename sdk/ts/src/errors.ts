import { ScruffExitCode } from "./types.js";

/**
 * Thrown by every SDK call that shells out and gets back a non-zero exit.
 * Carries scruff's actual exit code (SPEC.md §2.4) rather than collapsing it
 * to a generic failure — `code === ScruffExitCode.Refused` is how a caller
 * tells "scruff declined to destroy something" from "you asked wrong"
 * (`Usage`) or "registry locked" (`Locked`), and each deserves different
 * handling (retry, surface to a human, or just don't retry).
 */
export class ScruffError extends Error {
  readonly code: number;
  readonly stderr: string;
  readonly command: readonly string[];

  constructor(code: number, stderr: string, command: readonly string[]) {
    const label =
      code === ScruffExitCode.Usage
        ? "usage"
        : code === ScruffExitCode.Refused
          ? "refused"
          : code === ScruffExitCode.Degraded
            ? "degraded"
            : code === ScruffExitCode.Conflict
              ? "conflict"
              : code === ScruffExitCode.Locked
                ? "locked"
                : `exit ${code}`;
    super(`scruff ${command.join(" ")}: ${label}${stderr ? ` — ${stderr.trim()}` : ""}`);
    this.name = "ScruffError";
    this.code = code;
    this.stderr = stderr;
    this.command = command;
  }

  /** `true` when scruff declined for safety (occupied, dirty, or not provably
   * landed) rather than because the call itself was wrong. */
  get refused(): boolean {
    return this.code === ScruffExitCode.Refused;
  }

  /** `true` when the operation completed but a signal was unavailable (forge
   * down, no `lsof`) — check `warnings` on the envelope for why. */
  get degraded(): boolean {
    return this.code === ScruffExitCode.Degraded;
  }
}
