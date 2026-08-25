<div align="center">

# 🦦 holt

**the worktree-lifecycle substrate for parallel coding agents**

never loses work · never reaps what's in use · the registry, not the filesystem, is truth

</div>

---

Every vendor ships worktree *creation* now — `claude --worktree`, the Claude
Agent SDK's `isolation: worktree`, Cursor, Copilot CLI — and every one of them
stops there. Nobody owns the rest of the life: **the branch still alive after
the pane died**, **the checkout nobody is sitting in**, **the tree with 40
uncommitted minutes in it**, **the branch whose PR merged yesterday and has
kept committing since**.

If you've ever `git worktree list`'d through a graveyard trying to remember
which of those you can safely delete — that's the problem holt makes go away.

## install

```sh
nix run github:hausfold/holt                            # try it
nix profile install github:hausfold/holt                # keep it
go install github.com/hausfold/holt/cmd/holt@latest     # or, with Go 1.23+
```

Needs `git`. `gh` and `lsof` are optional and make it sharper — `gh` proves a
branch landed, `lsof` proves nobody's standing in a checkout; without them holt
degrades toward *keep*, never toward *delete*. On
[haus](https://github.com/hausfold/haus) it's already on `PATH`, wired to ⌘↵.

## a lane

One agent's **branch, checkout and pane**, from create to reaped. Not a
"worktree" — a parked lane has no checkout on disk, only a branch. Not a
"session" — that's your multiplexer's word.

```sh
cd "$(holt new fix-flaky-test)"  # a lane on this repo: new checkout, new branch, path on stdout
holt new fix --open        # …or hand the pane straight to your agent (--open codex, --cmd 'nvim')
holt                       # every lane you've got going, live or parked, across every repo
holt fix-flaky-test        # back later — rebuild the checkout, reopen the agent, where you left off
holt reap                  # sweep every lane whose branch landed and nobody's standing in
cd "$(holt child ../api)"  # a lane on a DIFFERENT repo — never a raw `git worktree add`
```

**And never `git stash` again.** The stash stack lives in the shared `.git`
dir, so every worktree of a repo *and* the main checkout push and pop the same
one — parallel agents routinely pop each other's work into a tree that never
asked for it. `holt park` commits your dirty tree as a single `wip:` commit on
the branch only this pane has checked out; `holt unpark` puts it back.

```sh
holt park "mid-refactor on the retry logic"
holt unpark
```

## the promise

Not "makes worktrees" — every vendor does that. holt's product is the state
machine and three invariants, in this priority order:

1. **Never lose work.** Every destructive path parks first. The failure
   direction is always *a branch lingers*, never *a tree vanished*.
2. **Never reap what's in use.** Occupied, dirty, or not provably landed ⇒
   keep. Uncertainty resolves to keep — including when GitHub is unreachable.
3. **The registry is truth.** Not the filesystem, not `git worktree list` —
   those are derived, and they lie.

So a command exiting **`2` — refused for safety** is holt working, not holt
failing. Don't reach for `git worktree remove`; ask it why.

## the verbs

| | |
|---|---|
| `holt` | list every live/parked lane, across all repos · `--json` for machines |
| `holt <name>` | resume one — rebuild its checkout, reopen its agent |
| `holt new [name]` | a lane on **this** repo — prints its path · `--open [agent]` / `--cmd '…'` to run something in it |
| `… --prompt '<task>'` | open it on a first turn instead of a blank pane · `--prompt-file <file\|->` for a brief · `--image <file>` |
| `holt child <repo>` | a lane on **another** repo, as a child of this pane |
| `holt spawn <repo> <name>` | a named lane, for a spawner with no pane of its own — `--prompt`/`--prompt-file` opens it through `[hooks] open` |
| `holt park [label]` / `unpark` | set the tree aside as a `wip:` commit, and put it back |
| `holt reap` | sweep every landed, unoccupied lane |
| `holt reaped` | what went, why, and the SHA to get it back |
| `holt drop <name>` | retire a lane that will never land — recorded, undoable |
| `holt reship [name]` | push a branch that outran its merged PR, open the follow-up |
| `holt heartbeat [path]` | hold the occupancy lease, so `reap` spares this lane |
| `holt watch --json` | lifecycle events on stdout, one NDJSON object per line |
| `holt runtime up\|enter\|down <name>` | hand a lane to an isolation backend, and take it back · `--backend <id>` every time, never automatic · `tart` is built in — a headless macOS per lane, so an agent can drive a desktop without taking yours |
| `holt runtime eject tart` | print the built-in backend as an adapter file, to edit and override it with |
| `holt hook create` / `remove` | client-hook entry points (this is what `claude --worktree` calls) |
| `holt hook notify` | Notification/Stop → a [trill](https://github.com/hausfold/trill) banner: an `ask` parked on the ledge when a lane blocks on its user, a `done` when it finishes · exit 0 always, silent no-op without trill |

`holt --help` is exhaustive. Config, exit codes, and the `--json` lane payload
are in [docs/reference.md](docs/reference.md).

## SDKs

Five clients over the same CLI, sharing one version number — `list`, `watch` as
a native async stream, `child`/`spawn`, `park`/`unpark`/`reap`/`reship`, and
occupancy leases.

| | | |
|---|---|---|
| **TypeScript** | `bun add @hausfold/holt` | [`sdk/ts`](sdk/ts) |
| **Python** | `uv add hausfold-holt` | [`sdk/python`](sdk/python) |
| **Rust** | `cargo add hausfold-holt` | [`sdk/rust`](sdk/rust) |
| **Go** | `go get github.com/hausfold/holt/sdk/go` | [`sdk/go`](sdk/go) |
| **Swift** | [`hausfold/holt-swift`](https://github.com/hausfold/holt-swift) | [`sdk/swift`](sdk/swift) |

<sub>⚠️ **Go:** the module path moved on 2026-08-16. `v0.2.8` and earlier stay
resolvable on Go's immutable proxy under the previous owner forever; everything
since is at `github.com/hausfold/holt/sdk/go`, so an existing importer edits its
import line. · **Swift** ships from a generated mirror — send changes to
`sdk/swift` here, never to the mirror.</sub>

## more

- [Lifecycle](docs/lifecycle.md) — states, landing rules, occupancy, and how to disagree with holt
- [Reference](docs/reference.md) — config, exit codes, the `--json` payload, building from source
- [`ai/SKILL.md`](ai/SKILL.md) — the agent-facing surface: drop it in and your agent drives holt correctly first try
- [`ai/handoff/SKILL.md`](ai/handoff/SKILL.md) — the companion skill: write a brief a cold session can act on, and `holt spawn --prompt-file` it into its own lane
- [SPEC.md](SPEC.md) — the design of record, and the contracts that are frozen
- [Releasing](docs/releasing.md) — how the CLI and all five SDKs are cut from one number

---

<div align="center">

<sub>**pre-1.0** · Nothing here takes something it can't give back — a dirty tree is parked as a commit, and only merged, unoccupied lanes are ever reaped. That's the intent, not a warranty: keep a backup, and [tell us what breaks](https://github.com/hausfold/holt/issues).</sub>

<sub>Apache-2.0 · one of the [hausfold](https://github.com/hausfold) repos — [haus](https://github.com/hausfold/haus) rebuilds the Mac, this is what its agent panes stand on</sub>

<a href="https://hausfold.co">⌂ hausfold</a>

</div>
