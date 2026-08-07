# 🦦 holt

**The worktree-lifecycle substrate for parallel coding agents.**

Every vendor now ships worktree *creation* — `claude --worktree`, the Claude
Agent SDK's `isolation: worktree`, Cursor, Copilot CLI — and every one of them
stops there. What nobody owns is the rest of the life: the branch that's still
alive after the pane died, the checkout nobody is sitting in, the tree with 40
uncommitted minutes in it, the branch whose PR merged yesterday and which has
kept committing since.

holt owns that.

```
create ──▶ live ──▶ parked ──▶ live ──▶ landed ──▶ reaped
             │        │                    ▲
             └────────┴────────────────────┘
```

The thing moving through those states is a **lane**: one agent's branch,
checkout and pane. Not a "worktree" — a parked lane has no checkout on disk at
all, and the branch is what survives. Not an "agent" either: that word is
reserved for the *client* a lane runs (`claude`, `codex`, `opencode`), and not
"session", which belongs to your multiplexer and to the clients' own transcripts.

Three invariants, in priority order — they are the product:

1. **Never lose work.** Every destructive path parks first. The failure
   direction is always "a branch lingers", never "a tree vanished".
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed
   means keep. Uncertainty resolves to *keep*, including when the forge is
   unreachable.
3. **The registry is the source of truth, and it is locked.** Not the
   filesystem, not `git worktree list` — those are derived, and they lie.

What you *do* at each transition — build, test, deploy — is yours. holt has no
opinion about your build system.

## Why not just use your agent's built-in worktrees

Because a vendor will never ship cross-client (nobody is going to support
`codex` **and** `opencode` **and** `claude` in one registry), never ship
cross-repo parentage, and treats the lifecycle invariants as an afterthought —
because losing *your* work isn't *their* problem.

## Status

**Pre-0.1.** The design is [`SPEC.md`](SPEC.md); it is the spec for a Go rewrite
of a 1295-line bash predecessor that had been running this author's machine as
Claude Code's worktree hooks for months, since retired entirely. holt was
extracted from the
[nebelhaus workshop](https://github.com/nebelhaus/workshop) incubator once it
passed that implementation's whole test suite.

**All 110 acceptance tests pass** (77 ported from that bash predecessor, two for
the bare-PATH hook environment, 14 for the policy seams, nine for occupancy
leases, and eight for `watch`). They are black-box, carried over from the bash
implementation — they drive the binary with shim `gh`/`lsof` on `PATH` and
never touch a real repo, so they describe the contract rather than the
implementation:

```
make test
```

Every command in the list below is implemented. What 0.1 still needs before
cutover is not features but *proof*: a week of dual-running against the shell
version on a real machine, and the hook switch behind one revertible option.

Then 0.2 — the adapter TOML that replaces the hardcoded client table, bootstrap
hooks with APFS/btrfs reflink, and `holt overlap`. Then 0.3 — `batch`, with
queue bisection. See [`SPEC.md`](SPEC.md).

## Non-goals

No scheduling. No agent supervision or restart. No fullscreen TUI. No hosted
anything. No knowledge of your build system, package manager, or CI. No merge
conflict *resolution*. No opinion about which agent you should run.

## Commands

```
holt                    list every live/parked lane, across all repos
holt <name>             resume one: rebuild its checkout, reopen its agent
holt new [name]         a lane on THIS repo, then open the default agent in it
holt child <repo>       a lane on ANOTHER repo, as a child of this pane
holt spawn <repo> <name>
                        a named lane for a spawner with no pane of its own
holt park [label]       set the working tree aside as a wip: commit on this branch
holt unpark             put the last parked commit's changes back, uncommitted
holt reap               sweep every LANDED lane that nobody is standing in
holt heartbeat [path]   hold the occupancy lease on a lane, so reap spares it
holt watch --json       lifecycle events on stdout, one NDJSON object per line
holt reship [name]      push a branch that outran its merged PR, open the follow-up
holt hook create        [hook] open a lane — JSON on stdin, path on stdout
holt hook remove        [hook] retire one without losing work — JSON on stdin
```

## Occupancy — telling holt a checkout is in use

`reap` never removes a lane somebody is standing in, which means it has to know
who is standing where. On a developer machine one `lsof` dump answers that:
a zellij pane has a shell cwd'd into the checkout. Anything else — a container,
a CI runner, a program embedding holt whose "sessions" are connections rather
than directories — has to say so itself:

```
holt heartbeat            # this checkout is in use while the calling process lives
holt heartbeat --release  # done with it
```

A lease naming a live pid is self-maintaining: the kernel releases it the moment
the holder dies, with no TTL to wait out and no refresh loop to write. Use
`--pid 0` when there is no local process to watch, and refresh within 90s.

One asymmetry is deliberate and load-bearing: **a lease can save a lane from the
sweep, never condemn one.** "Nobody leased it" is not evidence that nobody is
there — somebody may have just `cd`'d in. An orchestrator that genuinely owns
every session it serves can opt out with `HOLT_OCCUPANCY=lease`, and then an
unleased lane does count as free.

A lease is a client reporting on *itself*. The complementary case — a machine
that can enumerate everyone's sessions better than `lsof` can, and wants to
replace it outright — is a policy seam, and it's the next one to land.

## `holt watch` — embedding holt in something else

holt stays a binary; there is no daemon, no port, no socket to authenticate.
An embedder — an agent UI, a web server holding one session per lane — shells
out, same as a human at a terminal, and `watch` is the piece that makes that
enough: a long-running process emitting one NDJSON object per line on stdout
for as long as it runs.

```console
$ holt watch --json
{"kind":"hello","seq":0,"holt":"0.1.0","schema":1,"capabilities":["registry"]}
{"kind":"sync","seq":1,"ts":"2026-08-07T02:11:04Z","source":"registry","lane":{"name":"sparkle", …}}
{"kind":"ready","seq":2,"ts":"2026-08-07T02:11:04Z"}
{"kind":"created","seq":3,"ts":"2026-08-07T02:12:40Z","source":"registry","lane":{"name":"fresh", …}}
```

Every line after `hello` is one `kind`, at most one `lane`, and a `seq` that
counts the whole stream — hello included — so a consumer fanning this out over
its own transport (websockets, for a server holding many sessions on one
`watch`) can tell whether it dropped a line, without holt knowing anything
about that transport. `lane` is the exact shape `--json` uses for one entry in
`lanes[]` — one schema, whether you're reading a snapshot or a stream.

`sync` reports every lane already alive when the stream opened (your
baseline), then `ready` marks the end of that burst — everything after is a
live change: `created`, `parked`, `resumed`, `reaped`, or a catch-all
`changed` for anything else about a lane that differs from what this stream
last said (agent, dirty, landed, post-merge-ahead, last commit).

**What it doesn't cover, on purpose:** `created`/`parked`/`resumed`/`reaped`
are all registry mutations, so watching the registry file is a complete,
free signal for that family. `landed` and `post_merge_ahead` change at the
*forge*, with nothing local to watch — surfacing those here would mean
polling `gh` on a timer for as long as `watch` runs, and the one process this
is built for holds leases across many lanes and many repos at once, which
turns a timer into a rate-limit generator. So v1 emits registry-derived
events only; a consumer that cares about landedness polls `holt --json`,
same as today. `source` is on every event for exactly this reason — a
forge-derived family is additive later (`source: "forge"`, new `kind`
values) without a schema bump, and `capabilities` on `hello` is how a
consumer will be able to tell which families a given `holt` can ever send.

## Default agent

Set a durable default that works from Zellij, launchd, and a standalone terminal:

```toml
# ~/.config/holt/config.toml
agent = "codex"
```

`HOLT_AGENT` overrides that file for one invocation. Older nebelhaus installs
may supply `NEBELHAUS_AGENT_DEFAULT` as a compatibility fallback.

## Policy seams — disagreeing with holt

holt grew out of one machine's setup, and it inherited that machine's answers to
questions that only *look* universal. "Landed" means merged into the default
branch. "Reapable" means landed, clean and unoccupied. "Resume" means become the
client process. Each of those is right somewhere and wrong somewhere else, so
each is a **named seam** with holt's answer as the default rather than the
mechanism.

```toml
# ~/.config/holt/config.toml
[hooks]
resume   = "/usr/local/bin/my-resume"                     # a bare program
landed   = ["/usr/local/bin/my-landed", "--release-train"] # or an argv
```

| Seam | Answers | holt's default |
|---|---|---|
| `agent` | which client a new lane opens in | the `agent` key above |
| `landed` | has this branch's work reached the default branch? | ancestry → merged PR → patch-equivalence |
| `preserve` | does a closing pane's dirty tree become a `wip:` commit? | yes, unless it's untracked scratch on a landed branch |
| `resume` | reopen this lane's session | `cd`, then exec the client |
| `open` | open a session in a brand-new lane | `cd`, then exec the client |

A seam is a program. holt execs it, hands it the situation as JSON on stdin
*and* as `HOLT_*` environment variables, and reads the answer off the exit code:

```sh
#!/usr/bin/env bash
# a `resume` hook: open a zellij pane in the lane instead of taking over this
# process. $HOLT_CHAT, not $HOLT_PATH — a spawned lane's conversation lives in
# the pane that created it.
zellij action new-pane --cwd "$HOLT_CHAT" -- "$HOLT_AGENT" --resume
```

| exit | means |
|---|---|
| `0` | yes / handled |
| `1` | no / failed |
| `2` | no — refused for safety |
| `3` | **no opinion: run holt's built-in** |
| anything else | run the built-in, and warn |

Every failure mode falls back to the built-in, so a broken hook costs you the
override and never the operation — loudly, because a policy that silently
stopped applying is worse than one that never existed. A predicate can print a
JSON object on stdout to say more than yes/no: `{"via": "release-train"}` from a
`landed` hook keeps a reap attributable in `--json`.

Two things no seam can override, because they are about holt not sawing off the
branch it is sitting on: the checkout holt is being **run from** is never swept,
and a **stray** is never swept, only reported.

No `reapable` seam yet — it spans three of holt's opinions at once (occupancy,
dirtiness, landedness) and a `yes` on a dirty tree is the one answer that
destroys work, so it waits for the architecture those settle into. Overriding
`landed` already moves the rung that matters most.

`SPEC.md` §6.5 has the full contract and the list of facts still hardcoded.

### `park`, not `git stash` — the shared-stash-stack footgun

`git stash` looks per-checkout. It isn't. The stash is one ref — `refs/stash`,
with its reflog as the stack — and git's short list of per-worktree refs
(`HEAD`, `refs/bisect/*`, `refs/worktree/*`, …) does not include it. So every
worktree of a repo **and the main checkout** push and pop the *same stack*, and
with parallel lanes that means:

- Lane A stashes; lane B runs `git stash pop` in another checkout and receives
  A's changes into a tree that never asked for them — files B's agent has no
  context for, gone from the stack the moment they land.
- The "careful" form is racy too: `stash@{1}` is positional, so it names a
  different entry the instant any parallel lane pushes or drops one.
- A pop that conflicts leaves the entry both on the stack *and* half-applied —
  now two lanes can each believe they own it.

None of this bites a solo human, which is why nobody documents it: one popper,
one stack, no race. It starts biting the moment worktrees make your work
*parallel* — which is exactly what coding agents did.

`holt park [label]` is the same "hold this thought" with nothing shared: it
commits the whole dirty tree — untracked files included, `.gitignore`d ones
never — as one `wip:` commit on the branch only this pane has checked out (the
on-demand form of what the remove hook does on pane close). `holt unpark`
rewinds it, putting the changes back uncommitted. It refuses to unpark a wip
commit you've already pushed, so it can never become a force-push. Git lets a
branch be checked out in only one worktree at a time; that exclusivity is the
entire trick.

### Workspace trust is inherited, never invented

Claude Code keys its "do you trust the files in this folder?" dialog on the
**exact cwd**, in `~/.claude.json` — there is no inheritance from a parent
directory, and none from the git common dir. Its own `--worktree` doesn't prompt
because it seeds that key for the checkout it makes; a checkout *holt* made was
a directory Claude had never seen, so the same worktree of the same repo greeted
you differently depending on who ran `git worktree add`.

So when the client is Claude and the parent repo is **already trusted**, holt
copies that one boolean onto the new checkout. If the parent isn't trusted this
does nothing — holt propagates a decision you already made, it never makes one
for you — and it is a no-op for Codex and OpenCode, which have no such model.
Every failure (no config, unreadable, unparseable) is silent and costs exactly
one trust prompt, which is the behaviour it replaced.

### What "landed" means

The predicate that decides whether a branch **dies** handles every merge
strategy explicitly — fast-forward, merge commit, forge rebase, squash,
cherry-pick, merged-then-kept-committing — and degrades to *keep* whenever it
cannot prove the work is upstream. The full matrix is [SPEC.md §3](SPEC.md).

## Exit codes

| | |
|---|---|
| 0 | success, including "nothing to do" |
| 1 | usage / precondition error |
| 2 | **refused for safety** — occupied, dirty, or not provably landed |
| 3 | degraded — completed, but a signal was unavailable |
| 4 | conflict found (a finding, not an error) |
| 5 | registry locked by another holt |

`2` vs `1` is the one that matters: a wrapper script must be able to tell "you
asked wrong" from "I declined to destroy something".

## Building

```bash
make check
```

or `nix develop` for a shell with Go, bats and `gh`.

## License

Apache-2.0.
