# Reference

## Default agent

```toml
# ~/.config/scruff/config.toml
agent = "codex"
```

`SCRUFF_AGENT` overrides it for one invocation.

## Naming a lane after its task

A lane opened on a first-turn task (`--prompt` / `--prompt-file`) with **no name
of its own** can take its name from that task instead of from the animal list —
`hud-draft-color` rather than `cozy-otter`.

```toml
# ~/.config/scruff/config.toml
namer = "claude"
```

That is the whole setup, and there is no key by default: without one, an unnamed
lane still gets its random pair and no extra process runs at all. With one:

```sh
scruff spawn ~/code/bar --prompt-file brief.md    # the name argument becomes optional
scruff new --prompt 'the bar paints draft PRs in the merged green'
```

`claude` is the one built-in namer, for the same reason `tart` is the one
built-in runtime: it is the client a machine spawning agents already has. It runs
the `claude` binary on `PATH` — so the naming call is authenticated the same way
your agents are, with no API key for scruff to hold — on the cheapest model, with
the machine's MCP servers switched off:

```toml
kind = "namer"
id   = "claude"
name = ["claude", "-p", "--model", "haiku", "--strict-mcp-config", "--disable-slash-commands", "--", "{{.Prompt}}"]
```

Every other namer is a file with that shape, and a `claude.toml` of your own
shadows the built-in wholesale — a different model, a different client, a local
model, or a script with no model in it at all:

```toml
# ~/.config/scruff/adapters/namer/ollama.toml
kind = "namer"
id   = "ollama"
name = ["ollama", "run", "qwen3:4b", "{{.Prompt}}"]
```

**scruff never talks to a model.** It runs that one argv with the naming request as
`{{.Prompt}}` — the instruction, the repo, the lane names already taken and the
task, composed by scruff — and reads the answer off stdout. The command is expected
to print the name and nothing else; its stdin is `/dev/null` and its stderr is
scruff's, captured.

| | |
|---|---|
| a name you passed | always wins — the namer is only ever asked for a lane that has none, so `scruff new` and the ⌘↵ path never start a process |
| the shape of a name | one to three lowercase `[a-z0-9-]` words, 24 characters at most, with the repo's own name dropped — a listing already tells you which repo a lane is in |
| an answer that isn't one | rejected whole, never cleaned up: prose, a flag, a path, a traversal and anything non-ASCII all fall back to the random pair with a warning |
| anything else going wrong | same fallback — no adapter file, a namer that isn't installed, a run over 30s. **A namer can never cost you a lane**, only its name |
| what it costs | one model call on the create path. The built-in returns in 8–12s, most of it the client's own start-up |

## Runtime backends

`scruff runtime up|enter|down <name> --backend <id>` hands one lane to an
isolation backend — a VM, a container — and takes it back out again. Nothing
here is automatic: creating and reaping a lane never touch a backend, and there
is no default one, so a lane pays a VM's boot and disk only when someone asks
for it by name. `enter` replaces scruff with the session; the backend keeps
running until a separate `down`.

### `tart` — the one that ships built in

```sh
export SCRUFF_TART_BASE=ghcr.io/cirruslabs/macos-tahoe-base:latest   # or your own image
scruff runtime up    my-lane --backend tart    # clone it, boot it headless, wait for an address
scruff runtime enter my-lane --backend tart    # ssh in as $SCRUFF_TART_USER (default: admin)
scruff runtime down  my-lane --backend tart    # stop and delete the clone
```

A lane's guest is named `scruff-<lane>`, and the lane's worktree is shared into
it at `/Volumes/My Shared Files/work` — shared, not copied, so there is one
copy of the work. The guest boots `--no-graphics`: it runs a full WindowServer
and draws the real UI, it just draws it to nothing, which is the whole point
for an agent that needs to SEE a change without taking the display its user is
sitting at. `screencapture -x` over ssh returns real pixels from it.

Two environment variables and no config file: `SCRUFF_TART_BASE` (required — the
image to clone, because the images are tens of GB and which one you want is a
real choice) and `SCRUFF_TART_USER` (the guest account, `admin` by default, which
is what every cirruslabs base image ships). Needs `tart` on `PATH`; without it
the verb degrades with the install command rather than failing.

This is the only built-in, and it is one because it has to be: `setup` is a
clone, a backgrounded boot and a wait-for-address, which an argv slot cannot
hold — so leaving it to a file meant everyone writing the same script before
they could use the verb at all. A `tart.toml` you write still wins over it, and
`scruff runtime eject tart` prints a starting point.

### Every other backend is a file you write


```toml
# ~/.config/scruff/adapters/runtime/apple-container.toml
kind     = "runtime"
id       = "apple-container"
setup    = ["container", "run", "-d", "--name", "scruff-{{.Name}}", "-v", "{{.Path}}:/work", "IMAGE"]
enter    = ["container", "exec", "-it", "scruff-{{.Name}}", "bash"]
teardown = ["container", "rm", "-f", "scruff-{{.Name}}"]
```

Each value is an argv slice, not a command line: it is executed directly, with
no shell in the way, so a branch name with a space in it is one argument rather
than a quoting bug. The `{{…}}` variables are the set every adapter kind
shares — `Path`, `Main`, `Repo`, `Name`, `Branch`, `Base`, `Parent`, `Agent`
([SPEC.md §5.2](../SPEC.md#52-the-shared-template-variable-set)).

Three failures worth telling apart:

| | |
|---|---|
| no such adapter file | **2** — refused, naming the path it looked for. `tart` is the one id that falls back to a built-in instead |
| `--backend tart` with no `SCRUFF_TART_BASE` | **2** — refused, naming the image to pull. scruff never pulls tens of GB you didn't ask for |
| the backend's binary isn't on `PATH` | **3** — degraded. Install it and the same command works |
| the backend ran and exited non-zero | **1** — it attempted the thing and failed at it (a VM that already exists, a full disk, a bad image) |

## Environment

One spelling. This tool was `holt` until 1.0.0, and the `HOLT_*` fallback
rungs ended at 1.1.0 ([rename.md](./rename.md) §8.1) — the binary answers to
`scruff` alone now.

| | |
|---|---|
| `SCRUFF_AGENT` | the default client, for one invocation |
| `SCRUFF_BASE` | where checkouts live — default `~/.cache/scruff`, falling back to `~/.cache/claude-worktrees` only while that path holds the `registry.tsv` (see below) |
| `SCRUFF_STATE` | where machine state lives — the occupancy leases and the reap ledger (default `$XDG_STATE_HOME/scruff`, else `~/.local/state/scruff`) |
| `SCRUFF_OCCUPANCY` | `lease` declares that every session here is one this tool spawned, so a lane nobody leased is a lane nobody is in |

`SCRUFF_STATE` **must be absolute**. A relative value is refused with a warning
and the default is used: this state is machine-global, so resolving it against
the current directory would scatter the lease and the ledger into whatever
directory scruff was run from — routinely a git checkout, where they show up as
an untracked dir and can be swept into a `wip:` commit by `scruff park`.

A hook is handed the lane as `SCRUFF_*`, and none of the
above is among them: the lane's own fields are `SCRUFF_LANE_AGENT`,
`SCRUFF_LANE_STATE` and `SCRUFF_BASE_BRANCH`, spelled apart precisely so a hook's environment — which it
leaks into any pane it spawns — can never feed scruff back its own input. See
[lifecycle.md](./lifecycle.md).

## The base path

Checkouts live under `~/.cache/scruff/<repo>/<name>` whichever client you are.
The env ladder is `SCRUFF_BASE`, then `CLAUDE_WT_BASE` — the last rung
predates both of this tool's names and survives because SPEC.md §10's bash
predecessor is still the reason it exists.

A base at the legacy path (`~/.cache/claude-worktrees`) keeps working
indefinitely — the fallback keys on that path holding the `registry.tsv`, so
no one who skips the migration is broken. `scruff doctor --migrate-base`
moves it: it refuses with exit 2 while anything is standing in the base,
re-points every checkout with `git worktree repair`, rewrites the registry
under the same lock, and leaves the old path a symlink so stale absolute
paths still resolve ([rename.md](./rename.md) §8.2).

## Exit codes

| | |
|---|---|
| 0 | success, including "nothing to do" |
| 1 | usage / precondition error |
| 2 | refused for safety — occupied, dirty, or not provably landed |
| 3 | degraded — completed, but a signal was unavailable |
| 4 | conflict found |
| 5 | registry locked by another scruff |

## The `--json` payload

`scruff --json` (equivalently `scruff list --json`) prints one envelope:

```json
{ "scruff": "0.2.9", "schema": 1, "warnings": [], "lanes": [ … ] }
```

Each lane:

| Field | Meaning |
|---|---|
| `name` | lane name |
| `repo` / `main` | repo identity, and the main checkout's path |
| `branch` | full branch name |
| `path` | checkout path on disk — empty once `parked` |
| `parent` | the pane that spawned it via `scruff child`, or `""` |
| `agent` | `claude` \| `codex` \| `opencode` \| `pi` |
| `state` | `live` \| `parked` \| `stray` — a closed set |
| `occupied` / `dirty` | nullable; **`null` means undetermined, not false** |
| `occupied_by` | the evidence behind `occupied: true` — `[{pid, command, path, via}]`, `via` ∈ `lsof \| leases`. Absent when nothing holds the lane |
| `landed` | `{verdict: yes\|no\|fresh\|contained, via, confidence}` |
| `post_merge_ahead` | `{commits, pr, diverged}` — work done after the PR merged. An open PR at the tip already covers it, so the whole thing reads `{0, 0, false}` (`pr` is a plain int; absent is `0`, never `null`) |
| `last_commit` | most recent commit |

Two traps for consumers: reading `null` occupancy as clean, and ignoring
`warnings` — under `--json` the human-readable notes are suppressed, so
`warnings` is the only place a degraded run explains itself. This shape is a
frozen contract (`SPEC.md` §2).

`scruff` and `scruff --json` are **not read-only**: every listing also sweeps
landed parked branches. Harmless, but don't poll it thinking you're only
looking.

## Building

```bash
make check
```

or `nix develop` for a shell with Go, bats and `gh`.

## License

MIT — see [`LICENSE`](../LICENSE).
