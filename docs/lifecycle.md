# Lane lifecycle

```
create ──▶ live ──▶ parked ──▶ live ──▶ landed ──▶ reaped
             │        │                    ▲
             └────────┴────────────────────┘
```

A **lane** is one agent's branch, checkout, and pane, moving through that
life. Not a "worktree" — a parked lane has no checkout on disk; the branch
is what survives. Not an "agent" — that word means the *client* a lane runs
(`claude`, `codex`, `opencode`, `jcode`). Not a "session" — that's your multiplexer's.

## Invariants

1. **Never lose work.** Every destructive path parks first.
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed
   → keep.
3. **The registry is the source of truth** — not the filesystem, not
   `git worktree list`.

## States

| State | Meaning |
|---|---|
| `live` | checkout resolves; a pane may be sitting in it |
| `parked` | nothing on disk — the branch is the work. `holt <name>` rebuilds the checkout |
| `stray` | a directory git has disowned, from an interrupted `git worktree remove`. Reported, never swept |

## Landed

Whether a branch's work reached the default branch, across every merge
shape: fast-forward, merge commit, forge rebase, squash, cherry-pick, and
merged-then-kept-committing. Verdict is `yes` \| `no` \| `contained`, each
with a `via` and a `confidence`.

## Occupancy — heartbeats and leases

`reap` needs to know a checkout is in use. On a human's machine, `lsof`
answers that. Anything else — a container, a CI runner, an orchestrator —
says so itself:

```bash
holt heartbeat            # this checkout is in use while the calling process lives
holt heartbeat --release  # done with it
```

A lease naming a live pid self-expires the moment that process dies. Use
`--pid 0` and refresh within 90s when there's no local process to watch. A
lease can only ever *save* a lane from the sweep, never condemn one.

## Watch

`holt watch --json` streams lifecycle events — `created`, `parked`,
`resumed`, `reaped`, `changed` — as NDJSON, one object per line, for
anything embedding holt.

## Disagreeing with holt

Some answers are **policy seams**: a program you drop in that holt execs,
handing it the situation as JSON on stdin, reading the answer off the exit
code.

| exit | means |
|---|---|
| `0` | yes |
| `1` | no |
| `2` | no — refused for safety |
| `3` | no opinion — use the built-in |

```toml
# ~/.config/holt/config.toml
[hooks]
resume = "/usr/local/bin/my-resume"
landed = ["/usr/local/bin/my-landed", "--release-train"]
```

The situation arrives as `HOLT_*` in the environment and as JSON on stdin.
The `resume` and `open` seams also get `HOLT_CHAT` (the cwd the conversation
lives in — for a lane spawned by `holt child` that is the *parent's* checkout,
not the lane's) and `HOLT_COMMAND` (the exact client invocation holt was about
to run). A hook that opens a pane should run `HOLT_COMMAND` in `HOLT_CHAT`
rather than build its own, or it lands on a session picker `holt <name>` had
already resolved.

A broken or missing hook always falls back to the built-in. Two things no
seam can override: the checkout holt is run **from** is never swept, and a
**stray** directory is only ever reported.
