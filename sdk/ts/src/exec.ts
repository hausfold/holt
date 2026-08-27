import { spawn } from "node:child_process";
import { ScruffError } from "./errors.js";
import type { ScruffExitCode } from "./types.js";

export interface RunOptions {
  /** Path to the scruff binary, or a bare name resolved on PATH. Defaults to
   * `"scruff"`. */
  bin?: string | undefined;
  /** Working directory to run scruff from — most commands are cwd-sensitive
   * (`scruff new`, `scruff park`, a bare `scruff <name>`). */
  cwd?: string | undefined;
  /** Extra environment variables, merged over the current process's env —
   * e.g. `SCRUFF_AGENT`, `SCRUFF_OCCUPANCY`. */
  env?: Record<string, string | undefined> | undefined;
  /** Piped to the child's stdin, then the stream is closed. Used by
   * `scruff hook create`/`remove`, which read JSON off stdin (SPEC.md §2.3). */
  stdin?: string | undefined;
}

export interface RunResult {
  stdout: string;
  stderr: string;
  code: number;
}

/**
 * Runs one scruff invocation to completion and collects its output. Every
 * non-`--json` scruff command writes human text to stdout on success — this is
 * the primitive `list()`/`watch()` build their typed parsing on top of, and
 * the one lifecycle commands (`new`, `park`, `reap`, ...) use directly,
 * surfacing stdout as a plain string.
 *
 * Throws {@link ScruffError} on a non-zero exit, carrying scruff's exit code
 * (SPEC.md §2.4) rather than collapsing every failure into one shape.
 */
export async function run(args: string[], opts: RunOptions = {}): Promise<RunResult> {
  const bin = opts.bin ?? "scruff";
  const child = spawn(bin, args, {
    cwd: opts.cwd,
    env: opts.env ? { ...process.env, ...opts.env } : process.env,
    stdio: ["pipe", "pipe", "pipe"],
  });

  const stdoutChunks: Buffer[] = [];
  const stderrChunks: Buffer[] = [];
  child.stdout.on("data", (c: Buffer) => stdoutChunks.push(c));
  child.stderr.on("data", (c: Buffer) => stderrChunks.push(c));

  if (opts.stdin !== undefined) {
    child.stdin.end(opts.stdin);
  } else {
    child.stdin.end();
  }

  const code = await new Promise<number>((resolve, reject) => {
    child.on("error", reject);
    child.on("close", (code) => resolve(code ?? 1));
  });

  const stdout = Buffer.concat(stdoutChunks).toString("utf8");
  const stderr = Buffer.concat(stderrChunks).toString("utf8");

  if (code !== 0) {
    throw new ScruffError(code as ScruffExitCode, stderr, [bin, ...args]);
  }
  return { stdout, stderr, code };
}

/** Same as {@link run}, but parses stdout as JSON — for `--json` commands
 * only. scruff's own contract (README, internal/ui) is "stdout carries the
 * payload, every diagnostic goes to stderr", so this never has to guess
 * which lines are data. */
export async function runJSON<T>(args: string[], opts: RunOptions = {}): Promise<T> {
  const { stdout } = await run(args, opts);
  return JSON.parse(stdout) as T;
}
