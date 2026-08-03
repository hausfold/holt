# holt

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

**Pre-0.1, in the workshop incubator.** The design is [`SPEC.md`](SPEC.md); it
is the spec for a Go rewrite of `wt`, 1295 lines of bash that has been running
this author's machine as Claude Code's worktree hooks for months.

**All 79 acceptance tests pass** (77 ported from `wt`, plus two for the bare-PATH hook environment). They are black-box, carried over from the bash
implementation — they drive the binary with shim `gh`/`lsof` on `PATH` and never
touch a real repo, so they describe the contract rather than the implementation:

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
holt                    list every live/parked agent worktree, across all repos
holt <name>             resume one: rebuild its checkout, reopen its agent
holt new [name]         worktree of THIS repo, then open the default agent in it
holt child <repo>       worktree of ANOTHER repo, as a child of this pane
holt spawn <repo> <name>
                        a named worktree for a spawner with no pane of its own
holt park [label]       set the working tree aside as a wip: commit on this branch
holt unpark             put the last parked commit's changes back, uncommitted
holt reap               sweep every LANDED worktree that nobody is standing in
holt reship [name]      push a branch that outran its merged PR, open the follow-up
holt hook create        [hook] make a worktree — JSON on stdin, path on stdout
holt hook remove        [hook] retire one without losing work — JSON on stdin
```

### `park`, not `git stash`

The stash stack is **not** per-worktree. It lives in the shared `.git` dir, so
every worktree of a repo and the main checkout push and pop the *same* stack —
and parallel agents routinely pop each other's entries into a tree that never
asked for them. `holt park` commits the whole dirty tree as one `wip:` commit on
the branch only this pane has checked out. `holt unpark` rewinds it. It refuses
to unpark a wip commit you've already pushed, so it can never become a
force-push.

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
