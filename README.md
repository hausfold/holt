<div align="center">

# 🐈 scruff

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
which of those you can safely delete — that's the problem scruff makes go away.

## install

```sh
nix run github:hausfold/scruff                            # try it
nix profile install github:hausfold/scruff                # keep it
go install github.com/hausfold/scruff/cmd/scruff@latest     # or, with Go 1.23+
```

Needs `git`. `gh` and `lsof` are optional and make it sharper — `gh` proves a
branch landed, `lsof` proves nobody's standing in a checkout; without them scruff
degrades toward *keep*, never toward *delete*. On
[haus](https://github.com/hausfold/haus) it's already on `PATH`, wired to ⌘↵.

## a lane

One agent's **branch, checkout and pane**, from create to reaped. Not a
"worktree" — a parked lane has no checkout on disk, only a branch. Not a
"session" — that's your multiplexer's word.

```sh
cd "$(scruff new fix-flaky-test)"  # a lane on this repo: new checkout, new branch, path on stdout
scruff new fix --open        # …or hand the pane straight to your agent (--open codex, --cmd 'nvim')
scruff                       # every lane you've got going, live or parked, across every repo
scruff fix-flaky-test        # back later — rebuild the checkout, reopen the agent, where you left off
scruff reap                  # sweep every lane whose branch landed and nobody's standing in
cd "$(scruff child ../api)"  # a lane on a DIFFERENT repo — never a raw `git worktree add`
```

**And never `git stash` again.** The stash stack lives in the shared `.git`
dir, so every worktree of a repo *and* the main checkout push and pop the same
one — parallel agents routinely pop each other's work into a tree that never
asked for it. `scruff park` commits your dirty tree as a single `wip:` commit on
the branch only this pane has checked out; `scruff unpark` puts it back.

```sh
scruff park "mid-refactor on the retry logic"
scruff unpark
```

## the promise

Not "makes worktrees" — every vendor does that. scruff's product is the state
machine and three invariants, in this priority order:

1. **Never lose work.** Every destructive path parks first. The failure
   direction is always *a branch lingers*, never *a tree vanished*.
2. **Never reap what's in use.** Occupied, dirty, or not provably landed ⇒
   keep. Uncertainty resolves to keep — including when GitHub is unreachable.
3. **The registry is truth.** Not the filesystem, not `git worktree list` —
   those are derived, and they lie.

So a command exiting **`2` — refused for safety** is scruff working, not scruff
failing. Don't reach for `git worktree remove`; ask it why.

## the verbs

| | |
|---|---|
| `scruff` | list every live/parked lane, across all repos · `--json` for machines |
| `scruff <name>` | resume one — rebuild its checkout, reopen its agent |
| `scruff focus <name>` | go to the window a lane is already running in — `[hooks] focus`, resume without one |
| `scruff new [name]` | a lane on **this** repo — prints its path · `--open [agent]` / `--cmd '…'` to run something in it |
| `… --prompt '<task>'` | open it on a first turn instead of a blank pane · `--prompt-file <file\|->` for a brief · `--image <file>` |
| `scruff child <repo>` | a lane on **another** repo, as a child of this pane |
| `scruff spawn <repo> <name>` | a named lane, for a spawner with no pane of its own — `--prompt`/`--prompt-file` opens it through `[hooks] open` |
| `namer = "claude"` | opt-in: a lane opened on a task and given no name takes its name FROM that task — `hud-draft-color`, not `cozy-otter`. One config line; scruff runs the client already on `PATH`, never a model of its own |
| `scruff park [label]` / `unpark` | set the tree aside as a `wip:` commit, and put it back |
| `scruff reap` | sweep every landed, unoccupied lane |
| `scruff reaped` | what went, why, and the SHA to get it back |
| `scruff drop <name>` | retire a lane that will never land — recorded, undoable |
| `scruff reship [name]` | push a branch that outran its merged PR, open the follow-up |
| `scruff heartbeat [path]` | hold the occupancy lease, so `reap` spares this lane |
| `scruff watch --json` | lifecycle events on stdout, one NDJSON object per line |
| `scruff runtime up\|enter\|down <name>` | hand a lane to an isolation backend, and take it back · `--backend <id>` every time, never automatic · `tart` is built in — a headless macOS per lane, so an agent can drive a desktop without taking yours |
| `scruff runtime eject tart` | print the built-in backend as an adapter file, to edit and override it with |
| `scruff hook create` / `remove` | client-hook entry points (this is what `claude --worktree` calls) |
| `scruff hook notify` | client events → a [trill](https://github.com/hausfold/trill) banner for the lane: Notification hangs an `ask` on the ledge when a lane blocks on its user, Stop replaces it with a `done` when the turn finishes, and UserPromptSubmit / PostToolUse take an answered `ask` back down — the session moved again, so the question did too · every banner is keyed by lane, so one lane never hangs two · clicking it runs `scruff focus` on that lane · exit 0 always, silent no-op without trill |

`scruff --help` is exhaustive. Config, exit codes, and the `--json` lane payload
are in [docs/reference.md](docs/reference.md).

## SDKs

Five clients over the same CLI, sharing one version number — `list`, `watch` as
a native async stream, `child`/`spawn`, `park`/`unpark`/`reap`/`reship`, and
occupancy leases.

| | | |
|---|---|---|
| **TypeScript** | `bun add @hausfold/scruff` | [`sdk/ts`](sdk/ts) |
| **Python** | `uv add hausfold-scruff` | [`sdk/python`](sdk/python) |
| **Rust** | `cargo add hausfold-scruff` | [`sdk/rust`](sdk/rust) |
| **Go** | `go get github.com/hausfold/scruff/sdk/go` | [`sdk/go`](sdk/go) |
| **Swift** | [`hausfold/scruff-swift`](https://github.com/hausfold/scruff-swift) | [`sdk/swift`](sdk/swift) |

<sub>⚠️ **Go:** the module path has moved twice — `github.com/nebelhaus/holt`
through `v0.2.8`, `github.com/hausfold/holt` through `v0.5.0`, and
`github.com/hausfold/scruff` from `v1.0.0`. Each stays resolvable on Go's
immutable proxy at the tags published under it, so an importer on an old path
edits its import line. · **Swift** ships from a generated mirror — send changes
to `sdk/swift` here, never to the mirror.</sub>

## more

- [Lifecycle](docs/lifecycle.md) — states, landing rules, occupancy, and how to disagree with scruff
- [Reference](docs/reference.md) — config, exit codes, the `--json` payload, building from source
- [`ai/SKILL.md`](ai/SKILL.md) — the agent-facing surface: drop it in and your agent drives scruff correctly first try
- [`ai/handoff/SKILL.md`](ai/handoff/SKILL.md) — the companion skill: write a brief a cold session can act on, and `scruff spawn --prompt-file` it into its own lane
- [SPEC.md](SPEC.md) — the design of record, and the contracts that are frozen
- [Releasing](docs/releasing.md) — how the CLI and all five SDKs are cut from one number

---

<div align="center">

<sub>**pre-1.0** · Nothing here takes something it can't give back — a dirty tree is parked as a commit, and only merged, unoccupied lanes are ever reaped. That's the intent, not a warranty: keep a backup, and [tell us what breaks](https://github.com/hausfold/scruff/issues).</sub>

<sub>Apache-2.0 · one of the [hausfold](https://github.com/hausfold) repos — [haus](https://github.com/hausfold/haus) rebuilds the Mac, this is what its agent panes stand on</sub>

<a href="https://hausfold.co">⌂ hausfold</a>

</div>
