import { HoltExitCode } from "./types.js";

/**
 * Thrown by every SDK call that shells out and gets back a non-zero exit.
 * Carries holt's actual exit code (SPEC.md §2.4) rather than collapsing it
 * to a generic failure — `code === HoltExitCode.Refused` is how a caller
 * tells "holt declined to destroy something" from "you asked wrong"
 * (`Usage`) or "registry locked" (`Locked`), and each deserves different
 * handling (retry, surface to a human, or just don't retry).
 */
export class HoltError extends Error {
  readonly code: number;
  readonly stderr: string;
  readonly command: readonly string[];

  constructor(code: number, stderr: string, command: readonly string[]) {
    const label =
      code === HoltExitCode.Usage
        ? "usage"
        : code === HoltExitCode.Refused
          ? "refused"
          : code === HoltExitCode.Degraded
            ? "degraded"
            : code === HoltExitCode.Conflict
              ? "conflict"
              : code === HoltExitCode.Locked
                ? "locked"
                : `exit ${code}`;
    super(`holt ${command.join(" ")}: ${label}${stderr ? ` — ${stderr.trim()}` : ""}`);
    this.name = "HoltError";
    this.code = code;
    this.stderr = stderr;
    this.command = command;
  }

  /** `true` when holt declined for safety (occupied, dirty, or not provably
   * landed) rather than because the call itself was wrong. */
  get refused(): boolean {
    return this.code === HoltExitCode.Refused;
  }

  /** `true` when the operation completed but a signal was unavailable (forge
   * down, no `lsof`) — check `warnings` on the envelope for why. */
  get degraded(): boolean {
    return this.code === HoltExitCode.Degraded;
  }
}
