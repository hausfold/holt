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
| 1 | **Cutover version** | **`1.0.0`. Decided.** Renaming every package on five immutable registries is the largest break this repo will ever ship; there is no bigger number to save it for. |
| 2 | **`~/.cache/claude-worktrees` → `~/.cache/scruff`?** | **Yes. Decided — but it ships at `1.1.0`, not in the cutover.** The destination is settled so nothing has to be re-litigated; only the timing is deferred, and §8 specs the migration. Why not now: the path is the one surface where "final rename" and invariant 1 actually collide, and it is *not* holt's private business — haus reads `$BASE/registry.tsv` by hardcoded path and two shell hooks match `$HOME/.cache/claude-worktrees/*` prefixes, so this is a coordinated multi-repo move, not a `filepath.Join` edit. Doing it at `1.1.0` puts it on the one disruption boundary the plan already has instead of inventing a second. |
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
 P6  scruff 1.1.0 — the base move to ~/.cache/scruff, and the compat shims die.
```

Phases 1–5 are the rename. Phase 6 is the one migration it defers, and it is the
only phase that moves a byte of anyone's work on disk.

---

## 3. Phase 1 — `holt 0.5.0`, the bilingual release ✅ **built**

Ships from the repo **still named `holt`**. Purely additive; a machine that takes
this release notices nothing until it chooses to.

1. **Binary under both names.** ✅ `cmd/scruff/` is the real `main`, and `holt` is
   a **symlink onto the same binary** — not a second `main`. The program tells
   the two apart by `argv[0]` (`compat.InvokedByOldName`), so there is no second
   entry point to keep in step and no second build to get wrong. `flake.nix`
   installs the symlink in `postInstall`; the Makefile does the same for `make
   build`, and `overlays.default` exports `scruff`/`holt` **and**
   `scruff-skill`/`holt-skill` as the same derivations.
2. **Env vars: `SCRUFF_*` first, `HOLT_*` as a fallback rung.** ✅ All of them,
   through one helper (`internal/compat`) rather than a ladder per call site.
   `CLAUDE_WT_BASE` **keeps its priority above both** — it predates both
   spellings and answers to neither, which is SPEC §10's rung untouched.
3. **Both spellings are EXPORTED into every hook.** ✅ `config.hookEnv` emits the
   pair. This is the half an *old* consumer depends on: haus's lane hooks read
   `HOLT_NAME`, `HOLT_REPO`, `HOLT_PATH`, `HOLT_MAIN`, `HOLT_CHAT` and
   `HOLT_COMMAND` by those names, so a new binary emitting only the new spelling
   would blank the bar on an un-flipped machine.
4. **Config and state: `~/.config/scruff` and `~/.local/state/scruff` first.** ✅
   A **stat, never a move** — the old directory is used only when it is the one
   actually holding this machine's files. `~/.config/holt` is routinely a
   read-only symlink into a Nix store, so "just move it" was never available
   anyway. Adapters ride `config.Dir()` and come along for free.
5. **Registry stays at `$BASE/registry.tsv`.** ✅ Untouched. See decision 2.
6. **The skill derivation ships BOTH directories.** ✅ `$out/holt/SKILL.md` and
   `$out/scruff/SKILL.md`, with `name:` rewritten to match. This one was missing
   from the plan and is load-bearing — see the correction below.

**Verified, not assumed:** `make check` — 185/185 bats, all Go suites green;
`nix build .#default` produces `bin/scruff` + the `bin/holt` symlink;
`nix build .#scruff-skill` produces `handoff/`, `holt/` and `scruff/`;
`HOLT_BASE=… ./holt list --json` and `SCRUFF_BASE=… ./scruff list --json` agree
byte for byte; an existing `~/.config/holt` is still found and a fresh machine
writes `scruff`. Four new tests cover the ladder, the `CLAUDE_WT_BASE`
precedence, the dir fallback and the exported pair; two bats tests cover the two
binary names from outside the process.

**Ship it** as a normal `bench release holt 0.5.0` — the user's call, as always.
This release is the whole safety net; everything after it is recoverable.

### Three corrections this phase forced

Found by building it. The plan was wrong in three places:

- **The skill directory could not have flipped in Phase 2.** haus's
  `modules/ai/tool-skills.nix` names the directories it links (`names = [ "holt"
  "handoff" ]`) out of `$drv/<name>`, so flipping that list against a derivation
  that only ships `$out/holt` fails the build. Emitting **both** directories is
  therefore a Phase 1 job, not a Phase 2 one, and it is now done. Only ever one
  of the two is linked, so no agent sees the skill twice.
- **`cmd/holt/` is not renamed at Phase 3** — it was renamed here, and what
  Phase 3's move list should say is *delete the `holt` symlink at 1.1.0*.
- **The `--json` envelope key `"holt"` renames at Phase 3, not here**, and it
  should take `schema: 1` → `2` with it. Renaming a required envelope field is
  precisely the break `schema` exists to announce (SPEC §2.2). Deliberately
  *not* made bilingual: nothing in haus reads that key (checked — the bar and
  `lanes.sh` read `.lanes`), its only consumers are the five SDKs, and those are
  pinned packages that upgrade on purpose. A second envelope key would be two
  more fields across five SDKs to add now and remove later, for no safety.

**The deprecation notice is TTY-gated**, and that is a correctness property
rather than politeness: every non-interactive caller of this binary is one a
stray stderr line would hurt — Claude Code's `WorktreeCreate`/`Remove` and
`Notification` hooks, the bar plugins polling several times a minute, the
acceptance suite asserting on stderr, and anything parsing `--json`. The person
typing the old name is the only audience, and they are the only one at a
terminal.

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
4. **File and dir moves** (`git mv`, so history follows): `test/holt.bats` →
   `test/scruff.bats`, `sdk/python/src/holt/` → `sdk/python/src/scruff/`, and
   five `fake-holt.sh` → `fake-scruff.sh`. (`cmd/holt/` already moved in Phase 1.)
5. **The `--json` envelope key** `"holt"` → `"scruff"`, **and `schema: 1` → `2`
   with it** — renaming a required envelope field is exactly the break `schema`
   exists to announce (SPEC §2.2). Both structs in the CLI (`json.go`,
   `watch.go`) and both in each of the five SDKs. Nothing in haus reads it
   (checked); the SDKs are the consumers, and 1.0.0 is the signal.
6. **⚠️ OIDC trusted publishers will break and CI will fail on the first release
   attempt.** npm, PyPI and crates.io all match on **repo name and package name**,
   both of which just changed. Re-enter all three (`docs/releasing.md` has the
   table) *before* tagging, and update that table's package column.
7. Keep the `holt` symlink and the `HOLT_*` fallbacks from Phase 1.

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

## 8. Phase 6 — `1.1.0`: the base move, and the end of compat

One release later, and not before a `haus rebuild` has been green for a week.
Two things land together because they share a disruption boundary.

### 8.1 Delete the compat

Delete `internal/compat` and everything that imports it: the `holt` symlink and
its TTY-gated notice, the `HOLT_*` fallback rungs, the `HOLT_*` half of every
hook's environment, the old config/state path fallbacks, `$out/holt` from the
skill derivation, the `holt`/`holt-skill` overlay attributes, and the
both-spellings jq filters in haus and the host file. The grep that proves it is
`HOLT_`; the compile that proves it is `internal/compat` no longer existing.

### 8.2 `~/.cache/claude-worktrees` → `~/.cache/scruff`

This is the only step in the whole rename that moves work on disk, so it is
specified rather than described.

**Why it isn't a one-line default change.** The base path is a de-facto
cross-repo contract:

| consumer | how it depends on the path |
|---|---|
| every live lane | its `.git` file points at `<main>/.git/worktrees/<n>`, and that dir's `gitdir` file points **absolutely** back at the checkout |
| `$BASE/registry.tsv` | the registry itself lives in the base — the move is a registry move |
| `haus/modules/launcher/commands/spawn-agent.sh:117` | reads `${CLAUDE_WT_BASE:-$HOME/.cache/claude-worktrees}/registry.tsv` **by hardcoded path**, not by asking the binary |
| `haus/modules/terminal/default.nix:793, 856` | two shell hooks match on the `$HOME/.cache/claude-worktrees/*` prefix (stale-cwd detection, and the auto-`cd` out of a dead lane) |
| `haus/modules/ai/default.nix:204`, `workshop/AGENTS.md:164` | state the path as documentation an agent reads and believes |
| occupied panes | have a `cwd` *inside* the thing being moved |

**The migration, as `scruff doctor --migrate-base`:**

1. **Refuse if any lane is occupied.** Exit 2 — holt's own "refused for safety",
   not an error. You cannot move the ground out from under a live pane, and
   "close your panes and re-run" is a complete answer. This is invariant 2
   applied to the tool's own migration, which is the right way for it to behave.
2. Take the registry lock for the whole operation. It is one lock, one write.
3. `mv` the base, then `git worktree repair <path>` for every checkout — it is
   idempotent and it is exactly the operation git ships for this.
4. Rewrite `registry.tsv` paths in the same locked window, `.bak` first (the
   existing `registry.tsv.bak.relocate` convention already covers this shape).
5. Leave a symlink `~/.cache/claude-worktrees → ~/.cache/scruff` for one release,
   so anything holding a stale absolute path lands somewhere real.
6. On failure at any step, roll back to the `.bak` and leave the old tree in
   place. The failure direction stays "nothing moved", never "half moved".

**Fallback, permanently:** `baseDir()` prefers `~/.cache/scruff`, but falls back
to `~/.cache/claude-worktrees` when that exists and holds a `registry.tsv`. That
fallback is what keeps this a **minor** rather than a major — no one who skips
the migration is broken by it. The env ladder becomes `SCRUFF_BASE` →
`HOLT_BASE` → `CLAUDE_WT_BASE`; the last rung stays because SPEC §10's bash
predecessor is still the reason it exists.

**Run migration and haus in one rebuild:** the haus PR that flips
`spawn-agent.sh` and the two shell prefixes must land in the same `haus rebuild`
that ships `scruff 1.1.0`, or the bar reads a registry that has moved.

**Also here, and easy to forget:** the sentence "checkouts live under
`~/.cache/claude-worktrees/<repo>/<name>` whichever client you are — the path
name is historical" is *generated instructions* every agent on this machine
reads (`haus/modules/ai/default.nix:204`, mirrored into `workshop/AGENTS.md:164`
and `docs/reference.md:137`). After the move it is no longer historical and no
longer true — it becomes `~/.cache/scruff/<repo>/<name>`, and the apology for the
name goes away with it. That sentence disappearing is the actual finish line of
this rename.

---

## 9. The done-list

**At `1.0.0` — the rename is shipped when all of these are true:**

- [ ] `grep -ri holt` across all family repos returns **only** dated/historical statements
- [ ] `~/.claude/settings.json` contains no `holt` string, and a lane fires **one** notification
- [ ] ⌘↵ → spawn → park → resume → reap round-trips on a fresh `haus rebuild`
- [ ] all five SDK suites pass from their own directories
- [ ] `scruff --version` reports `1.0.0` (proves `LDFLAGS` and `go.mod` agree)
- [ ] npm/PyPI/crates show the deprecation pointer on the old names
- [ ] `hausfold/holt` and `hausfold/holt-swift` remain **unclaimed** on GitHub
- [ ] `hausfold.co` renders the family index with the accent colour intact
- [ ] `ai/SKILL.md` and `ai/handoff/SKILL.md` pass the `nix/skill.nix` guards under their new directory names

**At `1.1.0` — the rename is *done* when these are also true:**

- [ ] `scruff doctor --migrate-base` exits 2 with a lane occupied, and succeeds with none
- [ ] every lane resumes and `git status` cleanly from `~/.cache/scruff/<repo>/<name>`
- [ ] the bar, `spawn-agent.sh` and both shell hooks read the new base
- [ ] no `HOLT_`, `cmd/holt`, or both-spellings jq filter survives anywhere
- [ ] the "path name is historical" sentence is **deleted**, not updated — §8.2

---

## 10. Do it with the tool

Every phase after P1 is one lane per repo, spawned with `holt child <repo>` —
which is holt's own dogfooding rule and the only way the statusline can see the
child PRs while this is in flight. The last thing this tool does under its
old name is open the lanes that rename it.
