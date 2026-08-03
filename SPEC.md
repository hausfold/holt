# holt — design spec

**The worktree-lifecycle substrate.** A rewrite of the nebelhaus rice's `wt`
(`nebelhaus/modules/den/wt.sh`, 1295 lines of bash) as a standalone, repo-agnostic,
client-agnostic Go binary — a dev-focused sister to pounce / perch / trill, with
`nebelhaus` and `bench` demoted to consumers.

This is the design doc. No code yet. The bash `wt` keeps running inside nebelhaus,
untouched, until `holt` 0.1 exists and the hook switch flips.

Status: seed of a repo-to-be, in the workshop incubator (same pattern as
`incubator/flick`). It ejects to `nebelhaus/holt` when there's a binary beside it.

---

## 0. Thesis

Five agents in five worktrees is now normal. Every vendor ships worktree spawning
(`claude --worktree`, Claude Agent SDK `isolation: worktree`, Cursor, Copilot CLI)
and every one of them stops at *create*. What nobody owns is the rest of the life:
the branch that's still alive after the pane died, the checkout nobody is sitting
in, the tree with 40 uncommitted minutes in it, the branch whose PR merged
yesterday and which has kept committing since.

holt's product is not "make worktrees". It's the **state machine and its safety
invariants**:

```
        create ──▶ live ──▶ parked ──▶ live ──▶ landed ──▶ reaped
                    │         │                    ▲
                    └─────────┴────────────────────┘
                        (branch is the durable artifact;
                         the checkout dir is disposable)
```

Three invariants, in priority order. Everything else in this document is
subordinate to them:

1. **Never lose work.** Every destructive path parks first. The failure direction
   is always "a branch lingers", never "a tree vanished".
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed ⇒
   keep. Uncertainty resolves to *keep*, always, including when the forge is
   unreachable.
3. **The registry is the source of truth, and it is locked.** Not the filesystem,
   not `git worktree list` — those are derived and lie (stray dirs, half-removed
   checkouts, parked branches with no dir at all).

The actions at each transition — what to build, what to test, what to deploy —
belong to the user. holt has no opinion about your build system, and states so in
the README.

### Non-goals (say these in the README's second paragraph)

No scheduling. No agent supervision or restart. No fullscreen TUI. No hosted
anything. No knowledge of your build system, package manager, or CI. No merge
conflict *resolution*. No opinion about which agent you should run.

### The moat, stated plainly

Against first-party worktree support: vendors will never ship cross-client (no
one is going to support codex **and** opencode **and** claude in one registry),
never ship cross-repo parentage (`wt child` has no equivalent anywhere), and
treat the lifecycle invariants as an afterthought because losing *your* work
isn't *their* problem. Park, PR-verified reap, occupancy detection, and
post-merge drift detection are the product.

---

## 1. Name, license, distribution

| | |
|---|---|
| Name | **`holt`** — an otter's den; also a small wood. Free on npm and crates.io. Alternate: `copse`. |
| Why not `wt` | Already worktrunk's binary name, and Windows Terminal's. Non-negotiable rename. |
| Language | **Go.** Subprocess orchestrator, zero CPU-bound work — Rust/Zig buy nothing. CGo-free cross-compilation dominates for prebuilt-binary distribution. `x/sys/unix` has `Clonefileat` + `FICLONE` so reflink needs no CGo. charm (`fang`, `huh`, `lipgloss`) makes `doctor` and styled output good. Bun `--compile` measured 60 MB / 9 ms — startup fine, size not. |
| License | **Apache-2.0.** A commercial GUI must be able to embed the substrate (that's the thesis), and the patent grant matters for that. |
| Install CTA | `bun i -g holt` — an npm wrapper that downloads a prebuilt binary (the esbuild/biome pattern), **not** a bun-runtime tool. Also `brew install nebelhaus/tap/holt`, `curl … | sh`, and `go install`. |
| Tests | `nebelhaus/test/wt.bats` (1026 lines) is black-box — it drives the CLI with shim `gh`/`lsof` on `PATH`. **It ports unchanged** and becomes holt's acceptance suite on day one. That's the single best de-risking asset in the extraction. |

---

## 2. Public contracts — freeze these before anyone pins them

Everything in this section is versioned and breaking-change-gated once 0.1 ships,
because `bench`, the nebelhaus statusline, and pounce's "Spawn Agent" command all
pin them within a day of cutover.

### 2.1 Registry schema

Today (`$WT_BASE/registry.tsv`), one tab-separated line per worktree, six fields:

```
name    main-checkout    branch    checkout-path    parent    agent
```

Field 4 (checkout path) is the primary key. Field 6 is the client id
(`claude|codex|opencode`); **a row with fewer than 6 fields means `claude`** —
that's the already-shipped v0 migration case and it must survive.

**Rule for 0.1: read the existing file unchanged.** No format migration on
cutover day. Julien's machine has live rows written by bash `wt`; holt reads
them, writes them back byte-compatibly, and only *then* earns the right to
propose v1.

Proposed v1 (post-cutover, opt-in, `holt migrate`):

```toml
# $HOLT_STATE/registry/<sha256(checkout-path)[:12]>.toml   — one file per worktree
schema   = 1
name     = "sparkle"
repo     = "nebelhaus/nebelhaus"   # remote slug — see §4
main     = "/Users/j/code/workshop/nebelhaus"
branch   = "worktree-sparkle"
path     = "/Users/j/.cache/holt/nebelhaus-nebelhaus/sparkle"
parent   = "/Users/j/code/workshop"
agent    = "claude"
created  = 2026-08-03T10:04:00Z
```

Why one-file-per-row rather than a better TSV: it makes the lock story trivial
(create/rename is atomic on every filesystem holt targets), it kills the
read-modify-write race that `reg_put` currently papers over with a temp file +
rename of the *whole* table, and it lets a corrupt row be quarantined instead of
poisoning the parse. `schema = N` on every row; unknown-higher schema ⇒ holt
refuses to write and says which version to upgrade to.

**Locking.** 0.1 must take an exclusive `flock(2)` on `$HOLT_STATE/registry.lock`
for every mutation and a shared one for every read that will act on the result.
The bash version's TSV rewrite is a genuine lost-update race whenever two panes
close simultaneously; it's rare enough that it hasn't bitten, and that's luck.

### 2.2 `--json` output

Every listing/state command takes `--json`. One envelope, so consumers can
version-check without sniffing:

```json
{
  "holt": "0.1.0",
  "schema": 1,
  "worktrees": [
    {
      "name": "sparkle",
      "repo": "nebelhaus/nebelhaus",
      "main": "/Users/j/code/workshop/nebelhaus",
      "branch": "worktree-sparkle",
      "path": "/Users/j/.cache/holt/nebelhaus-nebelhaus/sparkle",
      "parent": "/Users/j/code/workshop",
      "agent": "claude",
      "state": "live",
      "occupied": true,
      "dirty": true,
      "ahead": 3,
      "behind": 12,
      "landed": { "verdict": "no", "via": null, "confidence": "certain" },
      "post_merge_ahead": { "commits": 0, "pr": null },
      "pr": { "number": 189, "state": "OPEN", "url": "https://…", "checks": "passing" },
      "overlap": ["frost"]
    }
  ],
  "warnings": ["forge unreachable: gh exited 4 — PR state is stale (cached 14m ago)"]
}
```

Contract points that matter:

- `state` ∈ `live | parked | stray`. Closed set; additions are minor, removals major.
- `landed.verdict` ∈ `yes | no | contained` and `landed.via` ∈
  `ancestry | pr-head-oid | patch-equivalence | merge-tree-empty | null` — see §3.
  Consumers must treat an unknown `via` as `no`.
- `occupied`, `dirty`, `pr` are **nullable**: `null` means *not determined*
  (no `lsof`, no forge, cache miss), which is categorically different from
  `false`. Every consumer bug in the bash version's statusline came from
  conflating those two.
- `warnings[]` is where degraded-mode explanations go. Never silently degrade.
- Field additions are non-breaking; consumers must ignore unknown keys.

### 2.3 Hook protocol

holt is the target of Claude Code's `WorktreeCreate` / `WorktreeRemove` hooks and
must stay tolerant of their drift. Today's bash `hook_field` accepts *either*
`name`/`worktree_name` and *either* `base_path`/`cwd` because the docs and 2.1.x
disagree. Keep that: **accept a set of aliases per logical field, first hit
wins**, and log (to `$HOLT_STATE/log`) which alias fired so a future CC bump is
diagnosable rather than silent.

```
holt hook create   < JSON on stdin  → the new checkout path on stdout, NOTHING else
holt hook remove   < JSON on stdin  → human text on stderr, nothing on stdout
```

The "only the path on stdout" rule is load-bearing (`cd "$(holt child …)"` and the
CC hook both depend on it). Every diagnostic goes to stderr. This is a contract,
not a style choice, and needs a test.

A generic `holt hook` also lets non-Claude clients wire in: the same JSON on
stdin from a Codex/OpenCode plugin gets the same behaviour.

### 2.4 Exit codes

The bash version has exactly two (0 / 1-via-`die`). Consumers need more:

| Code | Meaning |
|---|---|
| 0 | success — including "nothing to do" |
| 1 | usage / precondition error (bad args, not a git repo) |
| 2 | **refused for safety** — occupied, dirty, or not provably landed |
| 3 | degraded — the operation completed but a signal was unavailable (forge down, no `lsof`); pairs with a `warnings[]` entry |
| 4 | conflict found (`holt overlap`, `holt batch`) — a finding, not an error |
| 5 | lock contention / another holt holds the registry |

`2` vs `1` is the one that matters: a wrapper script must be able to distinguish
"you asked wrong" from "I declined to destroy something".

---

## 3. "Landed" — the merge-strategy matrix

This is the predicate the whole safety story rests on: it decides whether a branch
**dies**. Getting it wrong in the permissive direction destroys work. The bash
version already handles more of this than most tools; the spec is to keep every
existing signal and close the remaining holes explicitly.

| How the work got to the default branch | Tip is ancestor of default? | Forge record | Detected by |
|---|---|---|---|
| Fast-forward | ✅ | any | `merge-base --is-ancestor` |
| Merge commit | ✅ | MERGED | ancestry |
| Rebase-and-merge (forge button) | ❌ (new SHAs) | MERGED, `headRefOid` = pre-rebase tip = local tip | `headRefOid == local tip` |
| Squash-and-merge | ❌ | MERGED, `headRefOid` = local tip | `headRefOid == local tip` |
| Merged, then more commits on the branch | ❌ | MERGED, `headRefOid` ≠ tip | `post_merge_ahead` → `+N`, `holt reship` |
| Branch amended/rebased *after* its merge | ❌ | MERGED, `headRefOid` unreachable | count falls back to 1 — "at least one commit here didn't land" |
| Merged into a release branch, later to default | eventually ✅ | maybe | ancestry, once it arrives |
| Local `git merge --squash` + direct push, no PR | ❌ | none | **gap → merge-tree-empty (§3.2)** |
| Cherry-picked commit-by-commit | ❌ | none | **gap → patch-equivalence (§3.1)** |
| Merged from a fork | ❌ | `headRefName` may be `owner:branch` | **gap → §3.3** |
| PR merged >100 PRs ago | ❌ | outside the repo-wide `--limit 100` map | already safe: `branch_landed` keeps its own precise per-branch query, so the horizon only costs an annotation, never a wrong reap |
| Forge unreachable / no `gh` / offline | ❌ | unknown | **not landed** — keep. Correct, and stays correct. |

The existing division of labour is right and must survive the port: the **listing**
uses one repo-wide `merged_map` query (a per-branch query costs ~0.5 s each, which
turns a 0.3 s listing into seconds with eight worktrees), while **`branch_landed`
keeps its own exact per-branch query** because it decides whether a branch dies and
must not inherit the listing's horizon.

Likewise `default_branch` must keep resolving `refs/remotes/origin/HEAD` and never
`symbolic-ref HEAD` — the main checkout's current branch is not the branch a PR
lands on, and measuring against it once made a branch merged into a side branch
read as landed.

### 3.1 New signal: patch-equivalence

`git cherry <default> <branch>` marks every commit whose patch-id already exists
upstream with `-`. **All commits `-` ⇒ the work is upstream**, regardless of SHA.
Closes cherry-picks and rebases-done-elsewhere. Offline, no forge. Does *not*
close squashes (one squashed commit has no matching per-commit patch-id).

### 3.2 New signal: merge-tree-empty (`landed: contained`)

Strategy-agnostic and offline:

```
T = git merge-tree --write-tree <default> <branch>
T == tree-of(<default>)  ⇒  the branch adds nothing to the default branch
```

That's true for a squash merge, a manual re-implementation, and an empty branch
alike — which is exactly why it must **not** be a reap trigger by default. Spec:

- surfaced as `landed.verdict = "contained"`, `via = "merge-tree-empty"`,
  `confidence = "heuristic"`
- shown in `holt list` as `landed?` (with the `?`)
- `holt reap` ignores it unless given `--contained`, and even then requires
  clean + unoccupied + at least one commit not in default

Same primitive as §7 — one `merge-tree` implementation serves both features.

### 3.3 Fork PRs

`headRefName` for a cross-repo PR can arrive as `owner:branch`. `merged_map`'s
`$1==b` comparison silently never matches, so a fork-merged branch reads as
unlanded forever (safe, but it means the `+N` marker and the reap sweep both go
blind). Fix: match on the branch suffix after the last `:` **and** require
`headRepositoryOwner` to be a known remote before trusting an OID.

### 3.4 Degraded mode is a first-class state

If the forge is unreachable, holt must say so — `exit 3`, a `warnings[]` entry,
and a visible marker in the listing — not quietly report every branch as unlanded.
Silent degradation is how a user learns to distrust the tool.

---

## 4. Repo identity: the remote slug, not the directory basename

Today the bucket under `$WT_BASE` is `basename "$main"`, with one special case in
`wt child` that falls back to the owner-repo slug **only** when the child's
basename collides with the spawning pane's. That's a patch on a specific collision
(workshop `nebelhaus` vs rice `nebelhaus/nebelhaus`), and the original "fix" was
renaming a directory on one machine — which does not survive contact with
strangers' filesystems, where two `api` checkouts under different orgs are the
common case, not the exotic one.

**0.1: key every repo on its remote slug, always.**

```
identity = owner/name   from `git remote get-url origin`, scheme/user/host stripped
path key = owner-name   (slug with '/' → '-')
$HOLT_HOME/<owner-name>/<worktree-name>
```

- No `origin`? Try `upstream`, then the first remote alphabetically, then fall
  back to `local/<basename>` and record `repo = null` in the registry — degraded,
  works, and `holt doctor` tells you to add a remote.
- Multiple remotes disagreeing (fork workflows): `origin` wins; `holt doctor`
  reports the ambiguity.
- The bucket directory is **cosmetic**; every command re-derives a worktree's main
  checkout from the checkout itself (`git rev-parse --git-common-dir`), exactly as
  `resume_rows` does today. Never parse identity out of a path.

**Migration:** existing rows keep their existing `path`. holt reads them, resolves
them, and never rewrites a path under a live row — new worktrees get slug buckets,
old ones stay where they are. One `holt doctor --relocate` can offer to move them
later. Cutover day changes nothing on disk.

---

## 5. Adapters — one template-variable set, three kinds

Everything variable becomes a TOML file in a directory: **agent clients**, **forges**,
**runtime-isolation backends**. Built-ins ship as the same TOML, embedded via
`go:embed`, with no privileged code path — the built-in claude adapter is exactly
the file a user would write.

### 5.1 Resolution order

```
1. ~/.config/holt/adapters/<kind>/<id>.toml     — user
2. built-in (embedded)                          — shipped
```

**Repo-local adapters are forbidden in 0.1.** A repo contributing command
templates makes `git clone` + worktree-create a remote-code-execution path;
worktrunk hit the same wall and disables `--execute` in project hook bodies.
Per-user adapters cover every real case today. Loosening later behind a
direnv-style content-hash trust prompt is a *minor* release; tightening after
teams have committed `.holt/adapters/` is *breaking*. Ship tight.

A user adapter with the same id as a built-in shadows it wholesale (no merging —
merged config is unpredictable and undebuggable).

### 5.2 The shared template-variable set

Every adapter kind, every command template, gets the same variables. One table to
learn, one table to document.

| Variable | Meaning |
|---|---|
| `{{.Path}}` | the worktree checkout path |
| `{{.Main}}` | the main checkout path |
| `{{.Repo}}` | remote slug, `owner/name` |
| `{{.Name}}` | worktree name (branch minus the `worktree-` prefix) |
| `{{.Branch}}` | full branch name |
| `{{.Base}}` | default branch of the repo |
| `{{.Parent}}` | the spawning pane's cwd, or empty |
| `{{.Agent}}` | client id recorded for this worktree |
| `{{.Prompt}}` | initial prompt, when starting a client |
| `{{.Image}}` | path to an attached image, or empty |
| `{{.Port}}` | the deterministically allocated base port (§6) |
| `{{.Env}}` | map of the resolved environment |

Templates are `text/template` with **no** shell interpretation: each entry is an
argv slice, executed directly. No string-splitting, no quoting bugs, no injection
via a branch name containing a space.

### 5.3 Agent client — six lines

```toml
kind    = "agent"
id      = "amp"
start   = ["amp", "--cwd", "{{.Path}}", "--prompt", "{{.Prompt}}"]
resume  = ["amp", "--cwd", "{{.Path}}", "--continue"]
has_chat = ["test", "-d", "{{.Path}}/.amp"]     # exit 0 ⇒ a transcript exists
image_flag = "--image"                           # optional; omitted ⇒ name the file in the prompt
```

`has_chat` replaces the hardcoded "only Claude exposes a cheap cwd → transcript
test" special case: an adapter that omits it simply answers "unknown", and holt
falls back to the client's own cwd-filtered picker, which is today's behaviour for
Codex and OpenCode.

### 5.4 Forge — six lines

Detected from the remote host, so `gh`/`glab`/`tea`/`bb` is chosen automatically.
**holt never implements an auth flow of its own** — it delegates to whatever forge
CLI is on `PATH`, and falls back to the git-only merge-base check when none is.

```toml
kind  = "forge"
id    = "github"
hosts = ["github.com"]
probe = ["gh", "auth", "status"]
pr_for_branch  = ["gh", "pr", "list", "-R", "{{.Repo}}", "--head", "{{.Branch}}", "--state", "merged", "--limit", "1", "--json", "number,state,headRefOid"]
open_prs       = ["gh", "pr", "list", "-R", "{{.Repo}}", "--state", "open", "--limit", "100", "--json", "number,headRefName,headRefOid,title,url"]
```

Adapters declare **JSON-emitting** commands and holt maps them through a small
per-adapter key mapping (`number`/`state`/`head_oid`/`head_ref`), so `glab`'s
different field names are a config concern, not a code concern. Every forge call
goes through the existing 6-second timeout + on-disk cache — a stalled network
must never hang pane teardown.

### 5.5 Runtime isolation — six lines

Default backend is `none` + deterministic port/env allocation (§6). Containers are
**at most one optional backend, never the mechanism**.

```toml
kind  = "runtime"
id    = "apple-container"
setup = ["container", "run", "-d", "--name", "holt-{{.Name}}", "-v", "{{.Path}}:/work", "IMAGE"]
enter = ["container", "exec", "-it", "holt-{{.Name}}", "bash"]
teardown = ["container", "rm", "-f", "holt-{{.Name}}"]
```

---

## 6. Bootstrap & lifecycle hooks

A fresh worktree is useless if `node_modules` isn't there, `.env` isn't there, and
the dev server wants port 3000 that four other worktrees already want.

### 6.1 Hook points

`pre-create`, `post-create`, `pre-park`, `post-unpark`, `pre-reap`, `post-reap`.
Each runs argv-slices (no shell), with the §5.2 variables, and a non-zero exit on
a `pre-*` hook aborts the transition (exit 2 — refused).

### 6.2 Config file

`~/.config/holt/config.toml` for the machine-wide defaults; `<repo>/.holt.toml`
for the repo. **The split on repo-local config is by execution, not by file:**

| Repo-local key | Allowed? | Why |
|---|---|---|
| `copy`, `link`, `reflink` | ✅ | declarative paths, no execution |
| `ports`, `env` | ✅ | declarative values |
| `secrets` | ✅ | declarative paths, never contents |
| `run` / any hook body | ❌ unless trusted | it's execution — same RCE reasoning as §5.1 |

`holt trust` records a content-hash of `<repo>/.holt.toml`; a changed hash
re-prompts (direnv's model, applied to exactly the one dangerous key). Untrusted
`run` entries are *listed* by `holt doctor` — "this repo wants to run X; `holt
trust` to allow" — not silently dropped.

### 6.3 Built-in steps

```toml
[bootstrap]
reflink = ["node_modules", ".venv", "target", "vendor"]   # heavy gitignored dirs
copy    = [".env.local"]
link    = ["../shared-fixtures"]
ports   = { web = 3000, api = 8080 }
secrets = [".env.local", ".npmrc"]
run     = ["pnpm", "install", "--offline"]                # trust-gated
```

**reflink is the headline.** `cp -c` on APFS, `cp --reflink=auto` on btrfs/xfs,
`Clonefileat`/`FICLONE` via `x/sys/unix` with no CGo. It is strictly better than
both alternatives for the `node_modules` case:

| | cost | correctness |
|---|---|---|
| copy | seconds–minutes, GBs of disk | correct |
| symlink | instant, no disk | **broken** — pnpm and anything resolving `realpath` escapes into the source tree |
| **reflink** | instant, near-zero disk until write | correct, COW-isolated |

Fall back to copy with a `warnings[]` entry when the filesystem can't reflink;
never silently symlink.

**Ports:** `base = 20000 + (crc32(branch) mod 10000)`, then offset per named port.
Deterministic, so the same branch gets the same port every rebuild, and
collision-checked against the registry's other live allocations.

**Defer, don't fight.** If `.envrc` (direnv), `mise.toml`, `flake.nix` +
`.envrc`, or `.devcontainer/` is present, holt's default is to **do nothing** and
say so: those tools already own environment materialisation, and racing them
produces two half-configured environments. `holt doctor` reports "direnv detected
— holt is deferring; add `[bootstrap] force = true` to override."

**Secrets:** files listed under `secrets` are `chmod 600` on create and
shredded (overwrite + unlink) on reap. They're never copied into a park commit —
a `.gitignore`d secret must stay ignored, and `holt park` must refuse to `git add
-A` a file matching a `secrets` entry even if it's untracked-but-not-ignored.
That's a 5/5 failure mode and needs a test.

### 6.4 `holt doctor`

Inspects a repo and **writes a proposed config** — the single best onboarding
lever, because the alternative is reading a TOML reference.

```
holt doctor            # inspect, print findings + a proposed .holt.toml, write nothing
holt doctor --write    # write it
```

It detects: package manager and its heavy dirs; `.env*` files that are gitignored
(candidates for `copy`/`secrets`); ports in `docker-compose.yml` / `vite.config`
/ `package.json` scripts; direnv/mise/nix/devcontainer presence; whether the
filesystem supports reflink; whether a forge CLI is authenticated; whether `lsof`
or a heartbeat is available; submodules / LFS / sparse-checkout (§8); and the
default branch resolution. It also *diagnoses* — stale registry rows, stray
checkouts, orphan branches, disk used per repo.

---

## 7. `overlap` — conflict prediction, and the Clash question

### 7.1 What Clash is

[clash-sh/clash](https://github.com/clash-sh/clash): Rust, MIT, 63 stars, created
Feb 2026, last push mid-July. `clash check <file>` / `clash status` / `clash
watch`. It discovers worktrees via `git worktree list`, finds the merge base for
each pair, runs `git merge-tree` (via `gix`) in memory, and reports conflicting
files as a matrix. 100% read-only. `--json`, exit `0`/`2`/`1`. Ships a Claude Code
plugin that wires `clash check` as a **blocking `PreToolUse` hook on
`Write|Edit|MultiEdit`**.

### 7.2 Verdict: build a slim version inside holt; don't adopt Clash

The *idea* is correct and worth having. The *dependency* isn't, for four reasons:

1. **It's one git primitive.** `git merge-tree --write-tree A B` (git ≥ 2.38) does
   the whole thing and exits non-zero on conflict. That's ~200 lines of Go
   including the matrix rendering. Taking a second Rust binary, a second config
   file, and a second Claude plugin for that flatly contradicts holt's own "single
   binary, no runtime dependencies" pitch.
2. **It's structurally blind to exactly the worktrees that matter to holt.** Clash
   enumerates `git worktree list` — so it sees live checkouts only. holt's
   **parked** branches have no checkout on disk at all, and those are precisely
   the ones you've forgotten about and that have been rotting against `main` for a
   week. holt has a registry; it can merge-test a parked branch that Clash cannot
   see. That's not a bug in Clash, it's a capability holt has and Clash can't get.
3. **The blocking-hook integration is the wrong shape.** A `PreToolUse` prompt on
   every `Write|Edit` is a high-frequency interruption for a low-frequency event —
   and Clash's own README notes that Claude Code doesn't render
   `permissionDecisionReason`, so what you actually get is a bare permission
   prompt with no explanation of why. Across five agents that's unusable.
4. **Traction says it's a weekend idea, not infrastructure.** 63 stars, 1 fork,
   both Show HN posts sitting at 1 point, single maintainer. Fine tool. Not a
   dependency to hang a differentiator on.

MIT license means there is nothing to negotiate about implementing the same
approach — and the approach is one paragraph of public documentation, not IP.

### 7.3 What holt builds instead

```
holt overlap [--json] [--committed-only] [--pair A B]
```

- Pairwise `git merge-tree --write-tree` across **every registry worktree,
  including parked branches**, using each pair's own merge base.
- **Uncommitted work counts.** Clash's `has_active_changes` is a bare dirty
  boolean — it doesn't merge-test what the agents are currently typing, which in
  agent worktrees is *most of the interesting content*. holt builds a throwaway
  tree per worktree with `GIT_INDEX_FILE=$tmp git add -A && git write-tree` (the
  real index is untouched) and merge-tests those. That's the difference between
  "these branches will conflict eventually" and "your two running agents are
  fighting over `src/auth.ts` right now". `--committed-only` skips the worktree
  stat for speed.
- Output: conflicting file list per pair, plus the matrix. Exit 4 on conflicts
  found (a finding, not an error).
- **Passive surfaces, not blocking ones.** An `overlap` column in `holt list` and
  a token in the nebelhaus statusline. Optionally a *non-blocking* advisory hook
  that prints to the transcript. A blocking `PreToolUse` gate is available but is
  strictly opt-in and never the documented default.

Scaling is a non-issue and should be stated so: N is 3–8, merge-tree is
milliseconds, 12 worktrees is 66 pairs and still sub-second. Cache keyed on the
pair's `(tipA, tipB, mergeBase)` triple; the temp-tree path additionally keys on
worktree mtime.

### 7.4 The real payoff: `overlap` is stage 0 of `batch`

Conflict prediction as a standalone novelty is a demo. Conflict prediction as the
**free, offline prefilter for the expensive integration test** is architecture.
See §8.

---

## 8. `batch` — the differentiating feature

Generalises nebelhaus's `bench try-batch`. The problem it solves: the normal
review flow is merge-then-test, which puts unverified code on `main` before anyone
has felt it. `batch` inverts that.

```
holt batch [--verify "CMD"] [--json] [--land] [--exclude PR…]
```

Pipeline:

1. **Collect** open PRs via the forge adapter (`open_prs`), plus local
   `worktree-*` branches with no PR (opt-in via `--include-local`).
2. **Prefilter** with §7's pairwise merge-tree. Free, offline. Pairs that can't
   co-merge are recorded now, before a single integration worktree is created.
3. **Integrate**: a throwaway worktree off the default branch, merging candidates
   in a deterministic order (PR number ascending — reproducible, so the cache key
   means something), recording each conflicting pair as it's hit rather than
   aborting.
4. **Verify**: run the user-supplied command in the integration worktree. holt has
   no opinion about what it is. Nonzero ⇒ the set is red.
5. **Bisect the queue.** This is the feature. When verify fails on the full set,
   binary-search the *merge set* — not the commits — to name the culprit PR, or
   the culprit **pair** when no single PR is red alone. Halt at ~log₂(N) verify
   runs. Report "PR #182 alone is green; #182 + #177 is red" — that sentence is
   the entire value proposition, and no other tool in this space produces it.
6. **Report**: a tick-off checklist for humans, `--json` for machines. Each entry
   carries the PR's own Verify block from its body, so a failure sends you
   somewhere useful.
7. **`--land`** (opt-in, never default): merge only the green set, via the forge's
   PR merge — never a local merge + push.

**Cache** keyed on the merge-set hash (sorted tip OIDs + base OID + verify command
string). Re-running after one PR moves re-verifies only what changed.

Invariant: `main` is never touched. The integration worktree is disposable and
removed on completion, including on failure (behind `--keep` for debugging).

---

## 9. Portability gaps to close

| Gap | Today | 0.1 |
|---|---|---|
| **Occupancy** | one `lsof -d cwd` dump (~0.2 s), macOS/BSD-shaped, and unavailable in most containers | keep `lsof` as one *provider*; add a **heartbeat**: each `holt`-spawned client writes `$HOLT_STATE/live/<hash>.pid` with pid + mtime, refreshed by the wrapper; stale after 90 s. `occupied` becomes `true`/`false`/`null`, and `null` still means *keep*. On Linux, `/proc/*/cwd` is a third provider. |
| **Forge** | `gh` hardcoded, GitHub-shaped | §5.4 forge adapters; git-only merge-base fallback when none is present |
| **Submodules** | not initialised in a new worktree — `git worktree add` doesn't recurse | detect `.gitmodules`; `bootstrap.submodules = "recursive" \| "none"`; default `none` with a `doctor` warning, because recursing can be minutes |
| **LFS** | pointers, not files, unless a smudge runs | detect `.gitattributes` filter=lfs; offer `git lfs pull` as a bootstrap step; warn loudly rather than silently handing over pointer files |
| **Sparse-checkout** | not inherited from the main checkout | copy the main checkout's sparse patterns into the new worktree by default (`--no-inherit-sparse` to opt out) — inheriting is nearly always what's meant |
| **Disk accounting** | none | `holt list --disk` / `doctor` reports per-worktree and per-repo usage (`du`-equivalent, walked in Go); flag when reflink fell back to copy and the tree is >1 GB |
| **`python3` dependency** | `hook_field` shells out to python3 to parse hook JSON | gone — Go has `encoding/json` |
| **Registry race** | whole-table temp-file rewrite | per-row files + `flock` (§2.1) |
| **Windows** | not attempted | out of scope for 0.1; state it. Path handling should not gratuitously preclude it. |

---

## 10. The nebelhaus consumer story

### Keeps

- `modules/den/` keeps the **statusline** (it's rice-specific presentation) — it
  just consumes `holt list --json` instead of parsing TSV by hand.
- The `WorktreeCreate`/`WorktreeRemove` hook wiring, retargeted to `holt hook
  create` / `holt hook remove`.
- `bench` keeps `try`, `try-batch`, `ship`, `release`. `try-batch` becomes a thin
  wrapper over `holt batch --verify "bench try"` — bench supplies the nix
  knowledge, holt supplies the queue mechanics and the bisect.
- The zellij keybinds, pounce's "Spawn Agent" command, the `⌘C` seam.

### Deletes

- `modules/den/wt.sh` (1295 lines) in its entirety.
- `test/wt.bats` moves *out* of nebelhaus and into holt (it's holt's acceptance
  suite now; nebelhaus keeps only integration smoke tests for the hook wiring).
- The hand-maintained shell-side client list.

### `modules/lib/agents.nix` stops being a duplicate

Today `agents.nix` is the Nix-side list and `agent_known()` in `wt.sh` is a
hand-maintained shell-side copy of the same set — its own comment admits a fourth
client means editing both, because a shell script can't read Nix.

Fix: **Nix generates adapter TOML into a directory holt reads.**

```nix
# modules/den/holt.nix
environment.etc."holt/adapters".source = pkgs.runCommand "holt-adapters" { } ''
  mkdir -p $out
  ${lib.concatMapStrings (c: "cp ${adapterFor c} $out/${c}.toml\n") agentClients}
'';
```

with `HOLT_ADAPTER_PATH` including `/etc/holt/adapters`. `agents.nix` stays the
single source; the shell copy disappears; a fourth client is one list entry.

This also generalises: any Nix user gets declarative adapters for free, which is a
genuinely nice line in holt's README and costs nothing to support (it's just
another directory on the adapter path — see §5.1, extended to
`$HOLT_ADAPTER_PATH` entries between user and built-in).

### Cutover safety on Julien's machine

bash `wt` is load-bearing right now: two Claude Code hooks, the statusline
refresher, pounce's Spawn Agent command, and `bench`. So:

1. **holt reads the existing `registry.tsv` unchanged.** No format migration on
   cutover day (§2.1). This is the hard requirement.
2. The hook switch is **one nebelhaus option** —
   `nebelhaus.agents.worktreeBackend = "wt" | "holt"` — so the revert is
   `haus rollback`, not a code change.
3. Run both for a week: `holt` installed, option still `"wt"`, and a `holt list
   --json` vs `wt` diff check in `bench status`. Flip when they agree.
4. Only after the flip does `holt migrate` (registry v1) become available, and it
   backs up the TSV first.

---

## 11. Delta since the passoff — what landed in `wt` in the last few days

The passoff describes a 1092-line `wt`. It's **1295** now. These changes are all
general-purpose and belong in holt 0.1, not just in nebelhaus:

| Change | PR | Impact on this spec |
|---|---|---|
| `wt reship` + `+N` post-merge-ahead marker | #189 | New lifecycle state: *landed-but-moved-on*. Must be in the state machine (§0), the `--json` shape (`post_merge_ahead`, §2.2), and the merge-strategy table (§3). This is a genuinely novel state no competitor models. |
| Client-agnostic bar / tab badge / ⌘C | #170 | Confirms the agent-adapter seam (§5.3) is the right cut. |
| One client list in `modules/lib/agents.nix` | #171 | Directly motivates §10's "Nix generates adapter TOML". |
| Column sizing to pane / widest agent id | #168, 3a0d6d1 | Presentation belongs in the *consumer*; holt's job is `--json` + a good default renderer. |
| Worktree in a repo with **no commits yet** | #166 | `git worktree add --orphan`. A real edge case with a real fix — port it, and keep the test. |
| Codex + OpenCode support | #162 | Ditto §5.3. |
| Registry field 6 (`agent`), v0 rows = claude | #162 | The migration case that §2.1 must preserve forever. |

Anything landing in `wt` between now and holt 0.1 should be triaged against this
table: general ⇒ port it; rice-specific ⇒ it belongs in the consumer.

---

## 12. Milestones

| | Scope | Done when |
|---|---|---|
| **0.1** | Everything in §2 (contracts), §3 (landed, incl. patch-equivalence), §4 (slug identity), §5 (adapters), §10 (cutover). Commands: `list`, `new`, `child`, `spawn`, `resume`, `park`, `unpark`, `reap`, `reship`, `hook create/remove`, `doctor`. | `wt.bats` passes unmodified against `holt`; the nebelhaus option flips; a week of dual-running agrees. |
| **0.2** | §6 bootstrap (reflink, ports, secrets, trust), §7 `overlap`. | `holt doctor --write` produces a usable `.holt.toml` on a stranger's Node repo; `overlap` sees parked branches. |
| **0.3** | §8 `batch` with queue bisection; `bench try-batch` becomes a wrapper. | It names a culprit *pair* on a real red queue. |
| later | Runtime backends, `watch`, GUI-embeddable library split. | — |

---

## 13. Open questions

None blocking. The one thing to get right in the README's opening paragraph is the
positioning against first-party worktree support — see §0's "moat, stated plainly".
