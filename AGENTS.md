# AGENTS.md

**holt** — the worktree-lifecycle substrate for parallel coding agents. A single
Go binary plus five SDKs, published from this one repo under one version number.

**This file is the one set of instructions, for every agent.** Claude Code,
Codex, OpenCode, Cursor, Copilot — TUI or GUI — all read *this*, directly or
through a one-line pointer (`CLAUDE.md` is nothing but an `@AGENTS.md` import).
Nothing harness-specific belongs here; the wiring map is
[`.agents/README.md`](./.agents/README.md).

Deep detail lives in the docs, not here — [`SPEC.md`](./SPEC.md) is the design
of record, [`docs/lifecycle.md`](./docs/lifecycle.md) the state machine,
[`docs/reference.md`](./docs/reference.md) config and exit codes,
[`docs/releasing.md`](./docs/releasing.md) the six-artifact release.

## What holt is, and what it must never become

holt's product is **not** "make worktrees" — every vendor ships creation and
stops there. It is the **state machine and its safety invariants**. Three of
them, in priority order; everything else in this repo is subordinate:

1. **Never lose work.** Every destructive path parks first. The failure direction
   is always "a branch lingers", never "a tree vanished".
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed ⇒
   keep. Uncertainty resolves to *keep* — including when the forge is unreachable.
3. **The registry is the source of truth, and it is locked.** Not the filesystem,
   not `git worktree list` — those are derived, and they lie (stray dirs,
   half-removed checkouts, parked branches with no dir at all).

A change that trades any of these for convenience is wrong even when it passes
the suite. Say which invariant your change touches in the PR body.

**Non-goals, and they are load-bearing:** no scheduling, no agent supervision or
restart, no fullscreen TUI, no hosted anything, no knowledge of your build system
/ package manager / CI, no merge-conflict *resolution*, no opinion about which
agent you run. "Substrate, not orchestrator" is the whole thesis — the actions at
each transition belong to the user.

**holt is repo-agnostic and client-agnostic.** Nothing hausfold-specific ships in
it: no repo-local adapters, no `bench`, no rice paths, no assumption that the
consumer is the family. Adapters are template-driven (`SPEC.md` §5); if a need
can only be met by hardcoding one caller, it belongs in that caller.

## Vocabulary (the overload was the bug)

A **lane** is the unit — one agent's branch, checkout and pane, `create` →
`reaped`. Three words are reserved and mean only their narrow thing:

| word | means, and only this |
|---|---|
| **worktree** | git's — the checkout on disk. A *parked* lane has none, so it can't name the unit. |
| **agent** | the **client**: `claude`, `codex`, `opencode`. A lane *runs* an agent; it is not one. |
| **session** | somebody else's — the multiplexer's, or a client's transcript. holt never names its own unit this. |

`pane` is available, and is exactly what `occupied` reports.

## Frozen contracts — check before you touch

`SPEC.md` §2 is versioned and breaking-change-gated: the **registry schema**,
`--json` **output**, the **hook protocol**, and **exit codes**. Downstreams pin
them — the rice's statusline, the workshop's `bench status`, pounce's Spawn
Agent, and every SDK. Changing one is a semver **major** conversation, not a
refactor. The same goes for user-facing command names and flags.

## The five SDKs are one product

`sdk/{ts,python,rust,swift,go}` all wrap the same CLI and **share one version
number**. Five clients agreeing about one wire format is the invariant the SDK CI
job exists to protect, so a change to one SDK's surface is a change to all five.

- **`sdk/swift` is the source; [`hausfold/holt-swift`](https://github.com/hausfold/holt-swift)
  is a generated mirror** (`git subtree split`, synced by hand via
  `sdk/swift/sync-mirror.sh`). Never edit the mirror — changes there are
  overwritten on the next sync.
- **The Go module path keeps the `nebelhaus` owner on purpose.** `go.mod`,
  `sdk/go/go.mod` and the `Makefile`'s `LDFLAGS` all say
  `github.com/nebelhaus/holt`. It is published on Go's immutable proxy at that
  path and cannot move; GitHub's never-deleted org redirect is what keeps
  `go get` resolving. It is **not** a missed hit from the 2026-08-09 org move —
  don't "fix" it. Everything else in this repo says `hausfold`.

## Verify by running it

```sh
make check        # gofmt + go vet + go test ./... + the bats acceptance suite
make test         # just the suites
make build        # ./holt
```

The acceptance suite (`test/holt.bats`) is **black-box**: it drives the built
binary with shim `gh`/`lsof` on `PATH`, inherited from the bash predecessor so
the contract is provably unmoved. `go test ./...` covers the one thing a
black-box suite structurally can't — code that rewrites a file belonging to
another tool (Claude Code's `~/.claude.json`), where most of the assertion is
about what survived untouched. Both run in CI on macOS and Linux; a change that
only passes on one is not done.

## Releasing — always the user's call

**Never tag by hand.** Releases are cut from the workshop:

```sh
bench release holt 0.2.0        # stamps every manifest, commits, tags, watches CI
```

holt is the family's **one semver repo**, and it's forced, not chosen: three of
the five registries are immutable and already hold published numbers, so the
version is a compatibility contract rather than a date. Picking the bump means
reading `git diff <last-tag>..main -- sdk/` against the *published SDK surface* —
that judgement is [`docs/releasing.md`](./docs/releasing.md), and the release
itself is gated on the user. Propose the number and the evidence; don't run it
unprompted.

## Landing work

Standing rules for any agent working here:

- **Commit, push and open the PR without asking** — that's standing permission.
  Merging the PR is the user's call unless they say ship/land/merge.
- **Never push to `main` or `git merge` into it locally.** Parallel agents doing
  that have clobbered each other; a PR is atomic and conflict-detected.
- Give the PR a **What / Why / Verify / Watch-out** body — the session that wrote
  the code is gone by the time anyone feel-tests it, so `gh pr view` has to carry
  the recovery context. For this repo, **Verify** means the command someone else
  can run, and **Watch out** names the invariant or frozen contract you went near.
- Working on another repo from a holt pane? `holt child <repo>` — never a raw
  `git worktree add`, which skips the registry (holt's own dogfooding rule, and
  the reason the statusline can see a child's PR at all).

## Where holt sits in the family

The rice ([hausfold/hausfold](https://github.com/hausfold/hausfold)) takes holt
as a flake input and ships it on `PATH`; `⌘A` runs `holt new`, and Claude Code's
`WorktreeCreate`/`WorktreeRemove` hooks call `holt hook create` / `holt hook
remove`. Its bash predecessor `wt.sh` is retired — there is no fallback.

holt IS a family repo for *shipping* purposes — it's in the workshop's `FAMILY`,
so a merged commit here is invisible to the rice until `bench ship` bumps that
lock. But the dependency runs **one way only**: the rice consumes holt, and
nothing in this repo may know the rice exists. Keep it that way; the day holt
needs a hausfold-shaped special case is the day the abstraction was wrong.
