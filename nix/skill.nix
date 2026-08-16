# holt's agent skill, as a derivation.
#
# The source is `ai/SKILL.md` at the repo root — one file, committed, the same
# one that gets embedded in the binary when `holt skill` lands. This derivation
# is how a *consumer* gets it: haus takes holt as a flake input already, so
# `pkgs.holt-skill` is all its AI room needs in order to drop the skill into
# every agent client on the machine. A standalone user gets the identical bytes
# from `holt skill install`.
#
# Shape is fixed by the family standard (the workshop's notes/agent-surface.md):
# `$out/<name>/SKILL.md`, so a consumer links the directory and the skill's
# folder name is decided here rather than by whoever installs it. Skill names
# are globally unique across the family — they all land in one shared
# `~/.claude/skills/`.
{
  lib,
  runCommand,
}:

runCommand "holt-skill"
  {
    meta = {
      description = "Agent skill teaching a coding agent to drive holt's worktree lifecycle";
      license = lib.licenses.asl20;
      platforms = lib.platforms.all;
    };
  }
  ''
    mkdir -p "$out/holt"
    cp ${../ai/SKILL.md} "$out/holt/SKILL.md"

    # A skill with no frontmatter is invisible: every client routes on `name`
    # and `description`, and one that silently rendered without them would be
    # installed, listed, and never loaded — which looks exactly like the agent
    # not knowing holt exists. Fail the build instead of shipping that.
    head -1 "$out/holt/SKILL.md" | grep -qx -- '---' \
      || { echo "ai/SKILL.md does not open with YAML frontmatter" >&2; exit 1; }
    grep -q '^name: holt$' "$out/holt/SKILL.md" \
      || { echo "ai/SKILL.md has no 'name: holt' line" >&2; exit 1; }
    grep -q '^description: .\{80,\}' "$out/holt/SKILL.md" \
      || { echo "ai/SKILL.md's description is missing or too short to route on" >&2; exit 1; }
  ''
