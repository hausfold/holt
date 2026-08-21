---
name: holt
description: Manage git worktrees for parallel coding agents — list what's open, resume a parked lane, set work aside without git stash, sweep merged branches, recover something reaped. Use when the user says "what agents do I have running", "what worktrees are open", "resume that session", "park this", "stash this", "clean up merged worktrees", "I lost a branch", "where did that work go", or asks to start an agent on another repo. In a repo of theirs, prefer `holt park` over `git stash` — always.
---

# holt — the worktree-lifecycle substrate

holt gives every coding agent its own git worktree — its own checkout, its own
branch, its own pane — so parallel agents never fight over one working tree. One
of those is a **lane**: a branch, a checkout and a pane, from create to reaped.

Its product is not "make worktrees". It is the **state machine and its safety
invariants**, and those are what you must not undercut:

1. **Never lose work.** Every destructive path parks first. The failure
   direction is always "a branch lingers", never "a tree vanished".
2. **Never reap something in use.** Occupied, dirty, or not-provably-landed ⇒
   keep. Uncertainty resolves to *keep*.
3. **The registry is the truth.** Not the filesystem, not `git worktree list` —
   those are derived, and they lie.

holt is repo-agnostic and client-agnostic. It knows nothing about the build
system, the package manager or which agent is running.

## Verbs

| do this | run this |
|---|---|
| what's open, everywhere | `holt` |
| …machine-readable | `holt --json` |
| resume a parked lane | `holt <name>` (or `holt <repo>/<name>`) |
| a lane on this repo | `cd "$(holt new [name])"` |
| …and open an agent in it | `holt new [name] --open [agent]` |
| a lane on *another* repo | `holt child <repo>` |
| set the tree aside | `holt park [label]` |
| put it back | `holt unpark` |
| sweep landed lanes | `holt reap` |
| what got reaped, and the SHA to undo it | `holt reaped` |
| retire a lane that will never land | `holt drop <name>` |
| push commits that outran a merged PR | `holt reship [name]` |
| stand up/enter/tear down a lane's VM or container | `holt runtime up\|enter\|down <name> --backend <id>` |
| watch lifecycle events | `holt watch --json` (NDJSON, one object per line) |
| everything, exhaustively | `holt --help` (prints to **stderr**) |

`holt --json` returns `{holt, schema, warnings, lanes:[…]}`. Each lane carries
`name`, `repo`, `main`, `branch`, `path`, `parent`, `agent`, `state`,
`occupied`, `occupied_by`, `dirty`, `landed:{verdict,via,confidence}`,
`post_merge_ahead:{commits,pr,diverged}` and `last_commit`.

What an agent gets wrong about that payload:

- **`state` is a closed set: `live`, `parked`, `stray`.** A `stray` is a husk
  with no usable checkout — `holt <name>` moves it aside and rebuilds.
- **`landed.verdict` is `yes`, `no`, `fresh` or `contained` — and `fresh` is not
  `yes`.** A lane cut from the default branch that has never committed carries
  nothing of its own, so ancestry alone finds it already contained there.
  `fresh` is that case given its own word: report it as *nothing yet*, never as
  *merged*. A verdict or `via` you don't recognise resolves to not-landed.
- **`occupied` and `dirty` are nullable, and `null` means *not determined*, not
  false.** Reading `null` as falsy is how you tell a user a lane is clean when
  holt could not tell. Every consumer bug in the predecessor's status bar came
  from exactly this.
- **`occupied` says a process is standing there, not that a *pane* is.** A dev
  server or an orphaned daemon holds a lane exactly as hard as a live agent.
  `occupied_by[]` names it (`{pid, command, path, via}`, absent when free) — use
  it before telling a user to go and close a window that may not exist.
- **`warnings` is the only place a degraded run explains itself.** Under
  `--json` the human-readable notes are suppressed, so if you ignore
  `warnings` you will silently report a partial sweep as a complete one.

And `holt`/`holt --json` are **not read-only**: every listing also sweeps landed
parked branches. Harmless — the invariants still hold — but don't poll it under
the impression that you are only looking.

## Exit codes — check these, they mean different recoveries

| | meaning | what to do |
|---|---|---|
| 0 | success, **including "nothing to do"** | report what happened |
| 1 | usage or precondition error | fix the invocation |
| 2 | **refused for safety** — occupied, dirty, or not provably landed | this is holt working. Don't force it. |
| 3 | degraded — it completed, but a signal was unavailable | report the caveat |
| 4 | conflict found | the user resolves it |
| 5 | registry locked by another holt | another holt is mid-operation; retry shortly |

Exit 2 is the one to respect. It means holt decided keeping was safer than
removing, and the correct response is to say why, not to reach for `git
worktree remove`.

## When to reach for this

- "what am I working on / what's still open?" → `holt`, then read the states out
- "resume that session from yesterday" → `holt <name>`; it rebuilds the checkout
  and reopens the agent it was made with
- "park this, I need a clean tree" → `holt park`
- "clean up the merged ones" → `holt reap`, then `holt reaped` to show what went
- "I lost a branch" → `holt reaped` has the reason and the SHA to get it back
- "start an agent on the other repo" → `holt child <repo>` from this pane

## When NOT to

- **Never `git stash` in a repo the user owns — use `holt park`.** The stash
  stack lives in the shared `.git` dir, so every worktree of a repo *and* the
  main checkout push and pop the *same* stack; parallel agents routinely pop
  each other's entries into a tree that never asked for them. `holt park`
  commits the dirty tree as one `wip:` commit on the branch only this pane has
  checked out. This is the single most valuable thing in this file.
- **Never `git worktree add` directly** when a lane is what's wanted. A raw add
  skips the registry, so nothing downstream — status bars, `reap`, the parent
  pane — ever learns the worktree exists.
- **Never `git worktree remove` to "clean up".** That is what `reap` and `drop`
  are for, and they park first.
- **Don't ask holt to run, schedule or supervise an agent.** It is substrate,
  not orchestrator: no scheduling, no restarts, no opinion about which agent
  you run. The actions at each transition belong to the user.
- **Don't ask it about CI, merges or conflicts beyond detecting them.** It
  resolves nothing.

## Traps

- **A lane with zero commits is instantly sweepable.** A brand-new `holt
  new`/`holt child` checkout has no commits, so another session's `holt reap`
  can take it on ancestry. Commit something immediately.
- **`holt reap` deliberately spares a branch whose PR merged but which kept
  committing.** GitHub deletes the head branch on merge, so those later commits
  have no remote — that's `holt reship`, not a bug.
- **`holt unpark` refuses a wip commit you already pushed.** By design: it can
  never turn into a force-push.
- **The checkout is disposable; the branch is not.** Closing a pane parks and
  deletes the tree. Nothing is lost — say that rather than treating a missing
  directory as data loss.
