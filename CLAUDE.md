# CLAUDE.md

@AGENTS.md

<!--
Everything above this line is imported from AGENTS.md — the one set of project
instructions, shared by every harness. Put project rules THERE, not here, or
Codex/OpenCode/Copilot silently run without them.

Only Claude-specific wiring belongs below.
-->

## Claude-specific wiring (nothing project-level here)

| Thing | Where | Notes |
|---|---|---|
| Project instructions | `AGENTS.md`, imported above | Claude Code reads only `CLAUDE.md`, so this file exists purely to import it. |
| Session bootstrap | `.claude/settings.json` → `SessionStart` → `.agents/setup.sh` | Same script Codex and OpenCode call. Installs Nix in cloud containers, no-ops locally. |
| Tool allowlist | `.claude/settings.local.json` | Machine-local permission state, not a project rule. Yours; not committed as guidance. |
| Worktree hooks | `~/.claude/settings.json` (yours, not the repo's) → `holt hook create` / `holt hook remove` | **This repo is what those hooks call**, so a broken `hook create` breaks every agent pane on the machine — including the one you're in. Exercise hook changes by piping JSON to the built binary, never by opening a pane. That file is *declared by the rice* (`modules/terminal`, haus#201) and re-asserted every rebuild, so it self-heals when Claude Code rewrites it — which also means hand-editing it to work around a bug here is reverted by the next `haus rebuild`. Fix it in holt. |

The full cross-harness map is [`.agents/README.md`](./.agents/README.md).
