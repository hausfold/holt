# Reference

## Default agent

```toml
# ~/.config/holt/config.toml
agent = "codex"
```

`HOLT_AGENT` overrides it for one invocation.

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
