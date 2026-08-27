# holt → scruff — the cutover

The final rename. `holt` (an otter's den) becomes **`scruff`** — the loose skin a
mother cat carries a kitten by, where the kitten goes limp and is never dropped.
That image *is* invariant 1, and it moves the name out of the place-noun register
`holt` shared with nothing else in the family and into `pounce`'s verb register.

This document is the plan of record for that change across every repo, every
registry, and this machine. It is written to be executed by agents in parallel
lanes, one lane per repo, in the phase order below. **The phase order is the
safety property** — read §2 before touching anything.

> Scope note: the rename is *cosmetic in behaviour and total in surface*. No
> invariant, state machine, exit code or `--json` field changes meaning. Every
> break is a name.

---

## 0. Decisions to make first

Five, and four have a recommendation you can accept silently.

| # | decision | recommendation |
|---|---|---|
| 1 | **Cutover version** — `1.0.0` or `0.6.0`? | **`1.0.0`.** Renaming every package on five registries is the largest break this repo will ever ship; there is no bigger number to save it for, and "1.0" is the marketing moment the rename gives you for free. Counter: SPEC's `batch`/`overlap` are unbuilt, so 1.0 claims a completeness the feature set doesn't have. If that bothers you, `0.6.0` costs nothing but the moment. |
| 2 | **`~/.cache/claude-worktrees` → `~/.cache/scruff`?** | **No — not in this cutover.** `internal/commands/env.go:29` already defers this to "registry v1", and for a good reason: every live lane's `.git/worktrees/<n>/gitdir` holds an absolute path, so moving the base means a `git worktree repair` sweep across every checkout on the machine, and a failure there strands work. That is invariant 1 against a cosmetic gain. Ship it with registry v1, behind `scruff doctor --migrate-base`, with the old path read as a fallback forever. |
| 3 | **`worktree-<name>` branch prefix?** | **Leave it.** Nothing about it says "holt" — it's descriptive. Changing it strands every live branch and breaks `sed 's/^worktree-//'` in the host file and haus's lane scripts. |
| 4 | **Keep `holt` as a permanent alias?** | **No.** Ship it through `1.0.x` printing a deprecation to stderr, delete it at `1.1.0`. A permanent alias means the old name never leaves anyone's muscle memory or anyone's docs. |
| 5 | **Recreate `hausfold/holt` as a tombstone repo?** | **Never.** GitHub's rename redirect (web *and* git) lives only as long as the old name stays unclaimed. Creating a stub kills every redirect permanently — the same rule `ops/PRESENCE.md` records for keeping the `nebelhaus` org alive. Same for `holt-swift`. |

---

## 1. What is actually being renamed

Measured, not estimated (2026-08-27):

| surface | count | kind |
|---|---|---|
| `hausfold/holt` (this repo) | 2232 refs, 10 paths | code, docs, tests, 5 SDKs |
| `hausfold/haus` | 623 refs, 39 files | **wiring** — env vars, hooks, statusline, launcher, flake input |
| `workshop` root (`AGENTS.md`, `notes/`, `docs/`, `bench`, `test/`) | ~330 refs | family docs + the release path |
| `hausfold.co` | ~130 refs in `content/`, `src/`, `public/` | public docs + a CSS accent token |
| `hausfold/trill` | 57 refs | docs + two Swift doc-comments |
| `hausfold/ops` | ~34 refs | **mostly historical — see §6** |
| `~/.config/nix` (host file) | ~20 refs | this machine's own copy of the hook wiring |
| `perch` / `pounce` / `nebelung` / `homebrew-tap` | 12 refs total | prose only |

Three name-classes travel together and all three must move: **`holt`**,
**`Holt`** (Swift target, Go type docs), **`HOLT_`** (14 env vars). The string
`holt` appears inside no English word, so a scoped `sed` is safe here in a way it
usually isn't — the risk in this rename is *omission*, not over-matching.

---

## 2. Phase order — and why it is not negotiable

haus takes holt as a **flake input** and ships `pkgs.holt` on `PATH`. If holt's
overlay attribute disappears before haus stops asking for it, **haus stops
evaluating** — which means the machine cannot rebuild, which means the thing that
would fix it is the thing that's broken. Every phase below is ordered so that at
no point is a rebuild required to recover from a rebuild that failed.

The rule: **holt learns the new name before anything asks it the new name; holt
forgets the old name only after nothing asks it the old one.**

```
 P1  holt 0.5.0  — additive only. Answers to both names. Nothing renamed.
       ↓ (haus can now flip in either order, safely)
 P2  haus        — flips every call site to scruff. Binary still exists as both.
       ↓
 P3  holt 1.0.0  — the actual rename: repo, module, packages, mirror.
       ↓
 P4  family + web — docs, site, ops, workshop, trill.
       ↓
 P5  registries  — deprecate the old packages in place.
       ↓
 P6  holt 1.1.0  — delete the compat shims.
```

---

## 3. Phase 1 — `holt 0.5.0`, the bilingual release

Ships from the repo **still named `holt`**. Purely additive; a machine that takes
this release notices nothing.

1. **Binary under both names.** `cmd/scruff/` becomes the real `main`; `cmd/holt/`
   becomes a three-line shim that prints one deprecation line to stderr and
   `exec`s the same root command. The Nix package installs both, and
   `overlays.default` exports **`scruff` and `holt` as the same derivation**.
2. **Env vars: `SCRUFF_*` first, `HOLT_*` as a fallback rung.** All 14, in
   `internal/commands/env.go` and `runtime_tart.go`. Mirror the existing
   `CLAUDE_WT_BASE` → `HOLT_BASE` ladder exactly — it is the precedent and it
   already works. When holt *spawns* a lane it must **export both spellings**, or
   a new binary under an old haus blanks the bar.
3. **Config and state: `~/.config/scruff` and `~/.local/state/scruff` first**,
   old paths as fallback, no migration and no move.
4. **Registry stays at `$BASE/registry.tsv`.** Untouched. See decision 2.
5. **Adapters** resolve from the new config dir first, old second.

**Verify:** `make check`, then `HOLT_BASE=/tmp/x holt list` and
`SCRUFF_BASE=/tmp/x scruff list` must agree, and a lane spawned by the new binary
must show up in the bar on the *unmodified* haus.

**Ship it** as a normal `bench release holt 0.5.0`. This release is the whole
safety net; everything after it is recoverable.

---

## 4. Phase 2 — haus flips

One lane, one PR, on top of a `flake.lock` bumped to holt 0.5.0.

**Mechanical (39 files):** `haus/modules/{terminal,launcher,ai,bar,core}`, the
`holt` → `scruff` flake input rename, `modules/ai/holt-cache.sh` →
`scruff-cache.sh`, `modules/options-groups.nix:1125` (`cli = "holt"`), the
launcher palette entries (`default.nix:1768-1784`), `term-bindings.nix:145`, and
every `HOLT_*` read in the sketchybar plugins and `lanes/*.sh`.

**Regenerated, never hand-edited:** `haus/docs/site-data/options.json` and
`groups.json`.

**⚠️ Two settings.json traps — these are the ones that fail silently:**

- **Duplicate notification hooks.** `modules/terminal/default.nix:2085-2088`
  merges `holt hook notify` into four hook arrays using
  `map(select(... index("<the new command>") | not)) + [<the new command>]`. The
  filter matches only the string it is about to insert. Rename the command and
  the **old `holt hook notify` entries survive**, so every agent pane fires two
  notifications, forever, and no rebuild ever cleans them up. Fix: for one
  release, filter on **both** spellings before appending. The same jq is copied
  into `~/.config/nix/hosts/mbp/default.nix:1079-1088` — fix both.
- **Stale permission entry.** `default.nix:1108` and host file line 1107 append
  `"Bash(holt:*)"` under `| unique`, which never removes anything. Add
  `Bash(scruff:*)` and add a one-release `del`-style filter for the old one, or
  it lingers in everyone's settings.json indefinitely (harmless, but it is
  exactly the kind of residue "final rename" is supposed to preclude).

**`WorktreeCreate` / `WorktreeRemove` are safe** — they use assignment (`=`), not
append, so they self-heal.

**Also here:** the skill directory. `nix/skill.nix` ships `holt/SKILL.md`; the
build guard already fails if `name:` disagrees with the directory, so a
half-rename cannot ship — that guard is a free test, use it. `~/.claude/skills/holt`
becomes `~/.claude/skills/scruff`; `handoff/` is unaffected (it keeps its own name).

**Verify:** `haus rebuild` on this machine, then — in order — ⌘↵ spawns a lane,
the bar shows it, a notification arrives **once**, `scruff park` / `scruff` /
`scruff reap` round-trip, and `jq '.hooks' ~/.claude/settings.json` shows no
`holt` string anywhere.

---

## 5. Phase 3 — the rename itself, `1.0.0`

Now the repo is renamed and nothing on the machine depends on the old spelling.

1. **GitHub:** rename `hausfold/holt` → `hausfold/scruff`, and
   `hausfold/holt-swift` → `hausfold/scruff-swift`. Redirects handle existing
   clones and `Package.swift` URLs. Update the repo descriptions.
2. **Go module path** → `github.com/hausfold/scruff`, in `go.mod`,
   `sdk/go/go.mod`, and the `Makefile`'s `LDFLAGS` — ⚠️ a mismatch between the
   first and the last builds fine and reports the wrong version. This is the
   **second** freeze of a Go path for this project; `AGENTS.md`'s paragraph about
   the `v0.2.8` cutoff needs a second row, not a rewrite.
3. **Package names:** `@hausfold/scruff` (npm), `hausfold-scruff` (PyPI, crates —
   crate `name` plus `[lib] name = "scruff"`), Swift target `Holt` → `Scruff`,
   `sdk/swift/Sources/Scruff/`, `Tests/ScruffTests/`. All four are free
   (checked 2026-08-27).
4. **File and dir moves** (`git mv`, so history follows): `cmd/holt/` →
   `cmd/scruff/`, `test/holt.bats` → `test/scruff.bats`, `sdk/python/src/holt/` →
   `sdk/python/src/scruff/`, and five `fake-holt.sh` → `fake-scruff.sh`.
5. **⚠️ OIDC trusted publishers will break and CI will fail on the first release
   attempt.** npm, PyPI and crates.io all match on **repo name and package name**,
   both of which just changed. Re-enter all three (`docs/releasing.md` has the
   table) *before* tagging, and update that table's package column.
6. Keep the `holt` shim binary and the `HOLT_*` fallbacks from Phase 1.

**Verify:** `make check`, then each SDK's own suite from its own directory —
`bun test`, `pytest`, `cargo test`, `swift test`, `go test ./...` — because
`make check` structurally cannot see any of them.

---

## 6. Phase 4 — the family and the web

Independent of each other; run them as parallel lanes via `holt child`.

| repo | what moves |
|---|---|
| **workshop** | `AGENTS.md` (the family table, the lane section, the release rules), `notes/agent-surface.md`, `docs/`, `test/`, `script/`, and `bench`'s `FAMILY` entry + release path |
| **hausfold.co** | `content/docs/haus/rooms/ai.mdx` (59), `reference/options.mdx` (28), `internals/contributing.mdx`, `agent-rebuilds.mdx`; `src/app/page.tsx` (the family index entry + `data-accent`), `src/lib/shared.ts:37`. **⚠️ The CSS accent token `--a-holt` is defined in `public/hausfold.css` and consumed in `src/app/global.css:966` as `--nb-token-link` — rename it in both or a doc-link colour silently falls back.** No page slug contains `holt`, so **no redirects are needed** |
| **trill** | `ARCHITECTURE.md`, `AGENTS.md`, `CLAUDE.md`, `.gitignore` comment, and two doc-comments in `Trill/Platform/SystemIntegration.swift` (one of which states the `$HOME/.cache` registry fact — keep it accurate per decision 2) |
| **homebrew-tap / perch / pounce / nebelung** | prose only, 12 refs total |
| **~/.config/nix** | the host file (§4's jq, plus the `holt session` palette command at line 906 and the namer-adapter paths), then `nix flake update` for the renamed input — **never hand-merge `flake.lock`** |

**⚠️ `ops` is different — do not sed it.** `ops/scoreboard/data/*.json` are dated
snapshots and `PRESENCE.md` is a historical register; rewriting them falsifies
the record. Change only the **forward-looking** lines: `MARKETING.md`'s tape plan
(`holt/tape/holt.tape` → `scruff/tape/scruff.tape`), `TESTERS.md` row 8, and
`README.md:47`. Add one dated line to `PRESENCE.md` recording the rename and the
rule from decision 5. The same principle applies everywhere: **history keeps the
old name; live references take the new one.**

---

## 7. Phase 5 — the registries you can never take back

None of these can be renamed, deleted, or reclaimed. The old names are permanent
public artifacts; the job is to make them point somewhere.

| registry | action |
|---|---|
| npm | publish `@hausfold/scruff@1.0.0`, then `npm deprecate @hausfold/holt "renamed to @hausfold/scruff"` — this shows on install, which is the only channel that reaches someone |
| PyPI | publish `hausfold-scruff`, then one final `hausfold-holt` release whose description is the pointer. **Do not yank** — yanking is for broken releases and breaks pinned builds |
| crates.io | publish `hausfold-scruff`. crates.io has no deprecate; a final version with a redirect README is the convention. **Do not `cargo yank`** |
| Go | nothing to do, and nothing you *can* do. `github.com/hausfold/holt` stays resolvable at its pre-rename tags forever via the proxy cache and freezes there |
| SwiftPM | the `scruff-swift` mirror; existing `Package.swift` URLs keep resolving through GitHub's redirect. `sync-mirror.sh`'s `--prefix=sdk/swift` is unchanged |

---

## 8. Phase 6 — delete the compat, `1.1.0`

One release later, and not before a `haus rebuild` has been green for a week:
drop `cmd/holt/`, the `HOLT_*` fallback rungs, the old config/state path
fallbacks, and the both-spellings jq filters in haus and the host file. Leave
decision 2's `~/.cache/claude-worktrees` fallback in place — it is not compat,
it is the base path, and it goes with registry v1.

---

## 9. The done-list

The rename is finished when all of these are true:

- [ ] `grep -ri holt` across all family repos returns **only** dated/historical statements
- [ ] `~/.claude/settings.json` contains no `holt` string, and a lane fires **one** notification
- [ ] ⌘↵ → spawn → park → resume → reap round-trips on a fresh `haus rebuild`
- [ ] all five SDK suites pass from their own directories
- [ ] `scruff --version` reports `1.0.0` (proves `LDFLAGS` and `go.mod` agree)
- [ ] npm/PyPI/crates show the deprecation pointer on the old names
- [ ] `hausfold/holt` and `hausfold/holt-swift` remain **unclaimed** on GitHub
- [ ] `hausfold.co` renders the family index with the accent colour intact
- [ ] `ai/SKILL.md` and `ai/handoff/SKILL.md` pass the `nix/skill.nix` guards under their new directory names

---

## 10. Do it with the tool

Every phase after P1 is one lane per repo, spawned with `holt child <repo>` —
which is holt's own dogfooding rule and the only way the statusline can see the
child PRs while this is in flight. The last thing this tool does under its
old name is open the lanes that rename it.
