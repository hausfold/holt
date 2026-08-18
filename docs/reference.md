# Reference

## Default agent

```toml
# ~/.config/holt/config.toml
agent = "codex"
```

`HOLT_AGENT` overrides it for one invocation.

## Environment

| | |
|---|---|
| `HOLT_AGENT` | the default client, for one invocation |
| `HOLT_BASE` | where checkouts live (default `~/.cache/claude-worktrees`) |
| `HOLT_STATE` | where machine state lives — the occupancy leases and the reap ledger (default `$XDG_STATE_HOME/holt`, else `~/.local/state/holt`) |
| `HOLT_OCCUPANCY` | `lease` declares that every session here is one holt spawned, so a lane nobody leased is a lane nobody is in |

`HOLT_STATE` **must be absolute**. A relative value is refused with a warning
and the default is used: this state is machine-global, so resolving it against
the current directory would scatter the lease and the ledger into whatever
directory holt was run from — routinely a git checkout, where they show up as
an untracked dir and can be swept into a `wip:` commit by `holt park`.

A hook is handed the lane as `HOLT_*` too, and none of the above is among them:
the lane's own fields are `HOLT_LANE_AGENT`, `HOLT_LANE_STATE` and
`HOLT_BASE_BRANCH`, spelled apart precisely so a hook's environment — which it
leaks into any pane it spawns — can never feed holt back its own input. See
[lifecycle.md](./lifecycle.md).

## Exit codes

| | |
|---|---|
| 0 | success, including "nothing to do" |
| 1 | usage / precondition error |
| 2 | refused for safety — occupied, dirty, or not provably landed |
| 3 | degraded — completed, but a signal was unavailable |
| 4 | conflict found |
| 5 | registry locked by another holt |

## The `--json` payload

`holt --json` (equivalently `holt list --json`) prints one envelope:

```json
{ "holt": "0.2.9", "schema": 1, "warnings": [], "lanes": [ … ] }
```

Each lane:

| Field | Meaning |
|---|---|
| `name` | lane name |
| `repo` / `main` | repo identity, and the main checkout's path |
| `branch` | full branch name |
| `path` | checkout path on disk — empty once `parked` |
| `parent` | the pane that spawned it via `holt child`, or `""` |
| `agent` | `claude` \| `codex` \| `opencode` \| `jcode` |
| `state` | `live` \| `parked` \| `stray` — a closed set |
| `occupied` / `dirty` | nullable; **`null` means undetermined, not false** |
| `landed` | `{verdict: yes\|no\|contained, via, confidence}` |
| `post_merge_ahead` | `{commits, pr, diverged}` — work done after the PR merged |
| `last_commit` | most recent commit |

Two traps for consumers: reading `null` occupancy as clean, and ignoring
`warnings` — under `--json` the human-readable notes are suppressed, so
`warnings` is the only place a degraded run explains itself. This shape is a
frozen contract (`SPEC.md` §2).

`holt` and `holt --json` are **not read-only**: every listing also sweeps
landed parked branches. Harmless, but don't poll it thinking you're only
looking.

## Building

```bash
make check
```

or `nix develop` for a shell with Go, bats and `gh`.

## License

Apache-2.0 — see [`LICENSE`](../LICENSE).
