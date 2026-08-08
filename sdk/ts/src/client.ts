import { spawn } from "node:child_process";
import { run, runJSON, type RunOptions } from "./exec.js";
import { HoltError } from "./errors.js";
import { watchAll, watchLane } from "./watch.js";
import type { HoltEnvelope, WatchEvent, WatchLine } from "./types.js";

export interface HoltClientOptions {
  /** Path to the holt binary, or a bare name resolved on PATH. Defaults to
   * `"holt"`. */
  bin?: string;
  /** Working directory every command runs from — most of holt's commands are
   * cwd-sensitive (`new`, `park`, a bare `holt <name>`). Defaults to the
   * SDK process's own cwd. */
  cwd?: string;
  /** Extra environment variables, merged over the current process's env.
   * Useful for `HOLT_AGENT`, `HOLT_OCCUPANCY=lease`. */
  env?: Record<string, string | undefined>;
}

/**
 * A thin client over the `holt` binary. Every method shells out — there is
 * no daemon, no port, no socket (SPEC.md §14.1) — so this class holds
 * nothing but the options each call needs, and is cheap to construct as
 * often as you like.
 *
 * Two methods (`newInteractive`, `resumeInteractive`) inherit the calling
 * process's stdio and can hand off the terminal to a coding agent; every
 * other method captures output and returns. Mixing them up matters: see
 * each method's doc comment.
 */
export class HoltClient {
  private readonly opts: RunOptions;

  constructor(options: HoltClientOptions = {}) {
    this.opts = { bin: options.bin, cwd: options.cwd, env: options.env };
  }

  /** `holt --json` / `holt list --json` — byte-identical (SPEC.md §2.2).
   * The full snapshot: every live/parked lane, across every repo holt knows
   * about. Poll this for landedness and PR state; use {@link watch} for
   * everything else, since it's push rather than poll. */
  async list(): Promise<HoltEnvelope> {
    return runJSON<HoltEnvelope>(["--json"], this.opts);
  }

  /**
   * `holt watch --json` as an async iterable of typed lines — a `hello`,
   * then a `sync` burst for every lane already alive, `ready`, then live
   * changes for as long as you keep iterating. Stop iterating (`break`,
   * `return`, or dropping the generator) to kill the underlying process.
   *
   * This is the primitive `onOpen`/`onParked`/… callback-style APIs are
   * built from (SPEC.md §14.2) — see {@link watchLane} for a version
   * scoped to one lane's `path`.
   *
   * ```ts
   * for await (const line of holt.watch()) {
   *   if (line.kind === "created") console.log("new lane:", line.lane?.name);
   * }
   * ```
   */
  watch(): AsyncGenerator<WatchLine> {
    return watchAll(this.opts);
  }

  /**
   * {@link watch}, filtered to events about ONE lane (`event.lane.path`) and
   * stripped of the `hello`/`ready` framing that names no lane — the shape an
   * embedder holding one session per lane usually wants: "tell me when THIS
   * lane's state changes." A `sync` event for the lane still passes through:
   * it's how a caller that started watching after the lane went live learns it
   * exists at all.
   *
   * Compare full paths, not names: names aren't unique across repos, but a
   * checkout path is the registry's own primary key (SPEC.md §2.1).
   *
   * The module-level `watchLane` export does the same thing but takes its own
   * `RunOptions`; this one carries the client's `bin`/`cwd`/`env`.
   */
  watchLane(path: string): AsyncGenerator<WatchEvent> {
    return watchLane(path, this.opts);
  }

  /**
   * `holt child <repo> [name]` — a lane on ANOTHER repo, registered as a
   * child of `cwd`. Prints only the new checkout's path on stdout
   * (SPEC.md §2.3's "only the path" discipline extends here too) and never
   * execs a client, which is what makes it the right primitive for an
   * orchestrator: create the lane, then run your OWN agent process against
   * the path it returns.
   */
  async child(repoPath: string, name?: string): Promise<string> {
    const args = ["child", repoPath, ...(name ? [name] : [])];
    const { stdout } = await run(args, this.opts);
    return stdout.trim();
  }

  /** `holt spawn <repo> <name> [agent]` — a named lane for a caller with no
   * pane of its own (a scheduler, a web backend). Like {@link child}, only
   * ever creates the lane and prints its path; never execs. */
  async spawn(repoPath: string, name: string, agent?: string): Promise<string> {
    const args = ["spawn", repoPath, name, ...(agent ? [agent] : [])];
    const { stdout } = await run(args, this.opts);
    return stdout.trim();
  }

  /**
   * `holt <name>` / `holt resume <name>` with stdout captured rather than a
   * terminal — which means the Go binary's own TTY check (`ui.IsTTY`)
   * sees a pipe and, by design, never execs a client. It rebuilds the
   * checkout if needed and returns the human-readable result: either
   * confirmation it's ready, or the exact command to reopen the agent's
   * chat by hand. Safe to call from a server process. For a TUI that wants
   * to actually hand off the terminal, use {@link resumeInteractive}
   * instead.
   */
  async resume(name: string): Promise<string> {
    const { stdout } = await run(["resume", name], this.opts);
    return stdout;
  }

  /** `holt park [label]` — commits the working tree as one `wip:` commit on
   * the current branch. Never touches the shared stash stack (README's
   * "park, not git stash" section) — this is the one safe way for
   * concurrent lanes to set work aside. */
  async park(label?: string): Promise<void> {
    await run(["park", ...(label ? [label] : [])], this.opts);
  }

  /** `holt unpark` — reverses the most recent `park`, putting its changes
   * back uncommitted. Throws {@link HoltError} with `.refused === true`
   * if that commit is already pushed (holt will not rewrite published
   * history) or HEAD isn't a parked commit. */
  async unpark(): Promise<void> {
    await run(["unpark"], this.opts);
  }

  /** `holt reap` — sweeps every LANDED lane nobody is standing in (occupied,
   * per {@link heartbeat}/`lsof`, always wins). Never removes the checkout
   * holt is being run from, and never removes a stray. */
  async reap(): Promise<void> {
    await run(["reap"], this.opts);
  }

  /** `holt reship [name]` — pushes a branch that outran its already-merged
   * PR, and opens the follow-up. Throws with `.degraded === true` if `gh`
   * itself is unavailable. */
  async reship(name?: string): Promise<void> {
    await run(["reship", ...(name ? [name] : [])], this.opts);
  }

  /**
   * `holt heartbeat [path] [--pid N | --release]` — takes, refreshes, or
   * drops the occupancy lease on a checkout (SPEC.md §9.1, §14.2). This is
   * the seam built for exactly this SDK: a program embedding holt has no
   * pane and no shell cwd'd anywhere, so the lease is the only way `reap`
   * learns a checkout is in use. A lease can only SAVE a lane from the
   * sweep, never condemn one — see {@link HoltClient.lease} for a
   * self-refreshing wrapper instead of calling this on a timer yourself.
   */
  async heartbeat(path?: string, options: { pid?: number } = {}): Promise<void> {
    const args = ["heartbeat", ...(path ? [path] : [])];
    if (options.pid !== undefined) args.push("--pid", String(options.pid));
    await run(args, this.opts);
  }

  /** Drops the lease taken by {@link heartbeat}. */
  async releaseHeartbeat(path?: string): Promise<void> {
    await run(["heartbeat", ...(path ? [path] : []), "--release"], this.opts);
  }

  /**
   * Holds an occupancy lease for as long as the returned handle is open,
   * refreshing it on an interval comfortably under the 90s TTL
   * (`internal/occupancy.TTL`) that applies when there's no pid to watch.
   * This is the primitive an embedder's "session" (a connection, not a
   * cwd — SPEC.md §14.2) should hold from connect to disconnect:
   *
   * ```ts
   * const lease = holt.lease(laneDir);
   * // ... serve the session ...
   * await lease.release();
   * ```
   *
   * Pass `{ pid }` instead when the lease should track a real local
   * process — the kernel then releases it the instant that pid dies, with
   * no refresh loop needed at all, and `refreshMs` is ignored.
   */
  lease(path: string, options: { pid?: number; refreshMs?: number } = {}): Lease {
    return new Lease(this, path, options);
  }

  /**
   * `holt new [name] [agent]` with stdio INHERITED from the calling
   * process. holt execs the configured agent client unconditionally here
   * (unlike `resume`, `new` doesn't check for a TTY) — appropriate for a
   * real terminal app (a TUI) that wants to hand off the screen and get
   * control back when the agent session ends, and WRONG for a server: it
   * will block until the agent process exits, with your stdio attached to
   * whatever the agent expects.
   */
  async newInteractive(name?: string, agent?: string): Promise<void> {
    await runInteractive(["new", ...(name ? [name] : []), ...(agent ? [agent] : [])], this.opts);
  }

  /** `holt resume <name>` / `holt <name>` with stdio INHERITED, so a real
   * terminal's TTY check passes and holt hands off the screen to the
   * agent client. Same caveat as {@link newInteractive}: blocks until that
   * session ends. */
  async resumeInteractive(name: string): Promise<void> {
    await runInteractive(["resume", name], this.opts);
  }
}

function runInteractive(args: string[], opts: RunOptions): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(opts.bin ?? "holt", args, {
      cwd: opts.cwd,
      env: opts.env ? { ...process.env, ...opts.env } : process.env,
      stdio: "inherit",
    });
    child.on("error", reject);
    child.on("close", (code: number | null) => {
      if (code !== 0) {
        reject(new HoltError(code ?? 1, "", [opts.bin ?? "holt", ...args]));
      } else {
        resolve();
      }
    });
  });
}

/**
 * A held occupancy lease. See {@link HoltClient.lease}.
 */
export class Lease {
  private timer: ReturnType<typeof setInterval> | undefined;
  private released = false;

  constructor(
    private readonly client: HoltClient,
    private readonly path: string,
    private readonly options: { pid?: number; refreshMs?: number },
  ) {
    // A constructor can't be awaited, so a failed first heartbeat can't be
    // raised to the caller here. The `.catch` is load-bearing, not decoration:
    // `void`-ing a rejecting promise is still an UNHANDLED rejection, which
    // Node terminates the process over by default — so a lane path holt
    // refuses would take the embedder's whole server down with it.
    //
    // What that costs, stated honestly: on the refresh path the next tick
    // retries, so a transient failure self-heals and a permanent one keeps
    // failing visibly in holt's own logs. On the `pid` path there IS no next
    // call — no timer runs, and `release()` only ever calls
    // `heartbeat --release` — so a lease that never got taken looks exactly
    // like one that did. Call `client.heartbeat(path, { pid })` yourself first
    // if you need the initial take to be observable. (Go's `lease.go` and the
    // Rust SDK discard the first heartbeat the same way; Python's `lease()` is
    // a coroutine, so it alone can and does await it.)
    void client
      .heartbeat(path, options.pid !== undefined ? { pid: options.pid } : {})
      .catch(() => {});
    if (options.pid === undefined) {
      const refreshMs = options.refreshMs ?? 60_000; // < 90s TTL, with margin
      this.timer = setInterval(() => {
        void client.heartbeat(path).catch(() => {
          /* best-effort refresh; a miss self-heals on the next tick */
        });
      }, refreshMs);
      this.timer.unref?.();
    }
  }

  /** Drops the lease and stops refreshing it. Safe to call more than once. */
  async release(): Promise<void> {
    if (this.released) return;
    this.released = true;
    if (this.timer) clearInterval(this.timer);
    await this.client.releaseHeartbeat(this.path);
  }
}
