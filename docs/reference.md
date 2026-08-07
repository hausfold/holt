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

## Building

```bash
make check
```

or `nix develop` for a shell with Go, bats and `gh`.

## License

Apache-2.0 — see [`LICENSE`](../LICENSE).
