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
| Worktree hooks | `~/.claude/settings.json` (yours, not the repo's) → `holt hook create` / `holt hook remove` | **This repo is what those hooks call.** Claude Code owns that file and rewrites it, so no repo in the family touches it — but it does mean a broken `hook create` here breaks every agent pane on the machine, including the one you're in. Test hook changes with `holt hook create` on stdin, not by opening a pane. |

The full cross-harness map is [`.agents/README.md`](./.agents/README.md).
