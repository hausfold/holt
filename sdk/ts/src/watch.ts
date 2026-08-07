import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import type { RunOptions } from "./exec.js";
import type { WatchEvent, WatchLine } from "./types.js";

/**
 * `holt watch --json` as an async generator of typed lines. One object per
 * NDJSON line on stdout, in order: `hello`, a `sync` burst for every lane
 * already alive, `ready`, then live changes for as long as the process runs
 * (SPEC.md §14.3 step 2).
 *
 * The child process is killed when you stop consuming — `break` out of a
 * `for await`, call `.return()` on the generator, or let it fall out of
 * scope in a context that closes it. There is no other way to stop it
 * short: `watch` has no built-in end condition, by design (SPEC.md §14).
 */
export async function* watchAll(opts: RunOptions = {}): AsyncGenerator<WatchLine> {
  const child = spawn(opts.bin ?? "holt", ["watch", "--json"], {
    cwd: opts.cwd,
    env: opts.env ? { ...process.env, ...opts.env } : process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });

  const stderrChunks: Buffer[] = [];
  child.stderr.on("data", (c: Buffer) => stderrChunks.push(c));

  const rl = createInterface({ input: child.stdout, crlfDelay: Infinity });

  // Bridges readline's callback-based `line` event into something an async
  // generator can `for await`, without pulling in an event-stream library.
  const lines: string[] = [];
  let pendingResolve: (() => void) | undefined;
  let closed = false;
  let spawnError: Error | undefined;

  rl.on("line", (line: string) => {
    lines.push(line);
    pendingResolve?.();
  });
  rl.on("close", () => {
    closed = true;
    pendingResolve?.();
  });
  child.on("error", (err: Error) => {
    spawnError = err;
    closed = true;
    pendingResolve?.();
  });

  try {
    while (true) {
      if (lines.length === 0 && !closed) {
        await new Promise<void>((resolve) => {
          pendingResolve = resolve;
        });
        pendingResolve = undefined;
      }
      while (lines.length > 0) {
        const line = lines.shift();
        if (!line) continue;
        yield JSON.parse(line) as WatchLine;
      }
      if (closed) {
        if (spawnError) throw spawnError;
        return;
      }
    }
  } finally {
    rl.close();
    if (child.exitCode === null && !child.killed) {
      child.kill();
    }
  }
}

/**
 * {@link watchAll}, filtered to events about one lane (`event.lane.path`)
 * and stripped of `hello`/`ready`/`sync` framing — the shape an embedder
 * holding one session per lane usually wants: "tell me when THIS lane's
 * state changes." Compare full paths, not names: names aren't unique
 * across repos, but a checkout path is the registry's own primary key
 * (SPEC.md §2.1).
 */
export async function* watchLane(path: string, opts: RunOptions = {}): AsyncGenerator<WatchEvent> {
  for await (const line of watchAll(opts)) {
    if (line.kind === "hello" || line.kind === "ready") continue;
    if (line.lane && line.lane.path === path) {
      yield line as WatchEvent;
    }
  }
}
