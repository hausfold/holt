# Lane lifecycle

```
create ──▶ live ──▶ parked ──▶ live ──▶ landed ──▶ reaped
             │        │                    ▲
             └────────┴────────────────────┘
```

A **lane** is one agent's branch, checkout, and pane, moving through that
life. Not a "worktree" — a parked lane has no checkout on disk; the branch
is what survives. Not an "agent" — that word means the *client* a lane runs
(`claude`, `codex`, `opencode`, `pi`). Not a "session" — that's your
multiplexer's.

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
| `parked` | nothing on disk — the branch is the work. `scruff <name>` rebuilds the checkout |
| `stray` | a directory git has disowned, from an interrupted `git worktree remove`. Reported, never swept |

## Landed

Whether a branch's work reached the default branch, across every merge
shape: fast-forward, merge commit, forge rebase, squash, cherry-pick, and
merged-then-kept-committing. Verdict is `yes` \| `no` \| `fresh` \|
`contained`, each with a `via` and a `confidence`.

`fresh` is the one that is not about merging at all: the branch has never
carried a commit of its own, so there is nothing here to have landed. It is
still reapable — a never-committed branch has nothing to lose — but a
consumer that renders the verdict should say so as *nothing yet*, never as
*merged*.

## Occupancy — heartbeats and leases

`reap` needs to know a checkout is in use. On a human's machine, `lsof`
answers that. Anything else — a container, a CI runner, an orchestrator —
says so itself:

```bash
scruff heartbeat            # this checkout is in use while the calling process lives
scruff heartbeat --release  # done with it
```

A lease naming a live pid self-expires the moment that process dies. Use
`--pid 0` and refresh within 90s when there's no local process to watch. A
lease can only ever *save* a lane from the sweep, never condemn one.

## Watch

`scruff watch --json` streams lifecycle events — `created`, `parked`,
`resumed`, `reaped`, `changed` — as NDJSON, one object per line, for
anything embedding scruff.

## Disagreeing with scruff

Some answers are **policy seams**: a program you drop in that scruff execs,
handing it the situation as JSON on stdin, reading the answer off the exit
code.

| exit | means |
|---|---|
| `0` | yes |
| `1` | no |
| `2` | no — refused for safety |
| `3` | no opinion — use the built-in |

```toml
# ~/.config/scruff/config.toml
[hooks]
resume = "/usr/local/bin/my-resume"
landed = ["/usr/local/bin/my-landed", "--release-train"]
```

The situation arrives as `SCRUFF_*` in the environment and as JSON on stdin.
The `resume` and `open` seams also get `SCRUFF_CHAT` (the cwd the conversation
lives in — for a lane spawned by `scruff child` that is the *parent's* checkout,
not the lane's), `SCRUFF_LANE_STATE` (the lane's lifecycle state) and
`SCRUFF_COMMAND` (the exact client invocation scruff was about to run, **shell-quoted
per argument** — `scruff new`/`scruff spawn --prompt` put the whole task in there, and
a brief is multi-line and full of quotes and `$`).

The lane's own fields are spelled `SCRUFF_LANE_*` where scruff already reads the
plain name for itself — `SCRUFF_LANE_STATE` beside scruff's `SCRUFF_STATE` (its state
directory), `SCRUFF_LANE_AGENT` beside `SCRUFF_AGENT` (its one-invocation client
override), `SCRUFF_BASE_BRANCH` beside `SCRUFF_BASE` (its checkout base). A hook
leaks its whole environment into any pane it spawns, so a field that reused one
of those names would hand every later `scruff` in that pane its own input back. A hook that opens a pane should run `SCRUFF_COMMAND` in `SCRUFF_CHAT`
rather than build its own, or it lands on a session picker `scruff <name>` had
already resolved.

A seam also inherits the **caller's whole environment**, on top of those
fields — `cmd.Env` is `os.Environ()` plus them. That is the seam by which a
caller asks for something only this machine's hook can decide: how loudly to
open a window, which display to land on, whether to take the screen at all.
scruff neither sets nor reads any of it, and a hook that doesn't know a spelling
ignores it, so such a request costs nothing where it isn't understood. Keep
those names out of scruff: a variable scruff would have to know is a flag, and a
flag that only one consumer can honour belongs in that consumer.

A broken or missing hook always falls back to the built-in. Two things no
seam can override: the checkout scruff is run **from** is never swept, and a
**stray** directory is only ever reported.
