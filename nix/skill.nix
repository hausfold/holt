# scruff's agent skills, as a derivation.
#
# TWO skills, one derivation, one directory each:
#
#   ai/SKILL.md          → $out/scruff/SKILL.md    driving the lifecycle
#                        → $out/holt/SKILL.md      the same, under the old name
#   ai/handoff/SKILL.md  → $out/handoff/SKILL.md   filling a lane's first turn
#
# The second one is not a second copy of the first. `scruff` teaches an agent the
# verbs, the exit codes and the `--json` payload — what to run when the user
# says "what's open?". `handoff` teaches it the one thing that has no verb: how
# to write the brief a cold session can act on, whether that brief ends up on
# the clipboard or in `scruff spawn --prompt-file`. scruff is still substrate, not
# orchestrator — the skill describes handing work OVER, and stops there.
#
# What this split does NOT buy is hash isolation. `buildGoModule` has
# `src = ./.` with no filter, so `ai/` is inside the Go derivation's source
# closure and editing one line of prose moves scruff's drvPath either way
# (measured). What it does buy is real and is the reason it's separate: a
# consumer installs the skills without pulling the Go toolchain, and this
# derivation depends on those files rather than on the whole repo.
#
# `$out/<name>/SKILL.md` is the family standard's compliant-tool layout (the workshop's
# docs/agent-surface.md): one nesting level, named for the SKILL — which is
# the tool's own name when it ships one, and one directory per skill when it
# ships several. A consumer links a directory that is already called the right
# thing, and the TOOL decides those names rather than whoever installs it.
# Skill names are globally unique across the family: they all land in one shared
# `~/.claude/skills/`.
{
  lib,
  runCommand,
  bash,
}:

runCommand "scruff-skill"
  {
    nativeBuildInputs = [ bash ];
    meta = {
      description = "Agent skills teaching a coding agent to drive scruff and to hand work to a fresh lane";
      license = lib.licenses.asl20;
      platforms = lib.platforms.all;
    };
  }
  ''
    # The whole ai/ tree, not two named files: the layout below is DERIVED from
    # it, so a third skill needs no edit here, in check.yml, or in the guard
    # script. Three hardcoded lists is three places to forget one — and
    # forgetting it in the CI copy reinstates the exact gap the guards exist to
    # close, since a skill that is never checked is one that installs, lists and
    # is never loaded.
    ai=${../ai}

    mkdir -p "$out/scruff"
    cp "$ai/SKILL.md" "$out/scruff/SKILL.md"

    # The same skill under the OLD name, for the length of the rename
    # (docs/rename.md §3, deleted at §8.1). It is here rather than in the
    # consumer because the INSTALLER links `$out/<name>`: haus's
    # tool-skills.nix names the directories it wants, so a consumer that has
    # not flipped yet asks for `holt` and must still find a directory.
    #
    # `name:` has to be rewritten with the directory, or the guard below fails
    # on the copy: a skill whose frontmatter name disagrees with its directory
    # installs, lists, and is never loaded. Only ONE of the two is ever linked
    # into ~/.claude/skills, so no agent sees the skill twice.
    #
    # Both this block and $out/holt go at 1.1.0 (§8.1), leaving one directory
    # named scruff.
    mkdir -p "$out/holt"
    sed '0,/^name: scruff$/s//name: holt/' "$ai/SKILL.md" > "$out/holt/SKILL.md"
    grep -q '^name: holt$' "$out/holt/SKILL.md" || {
      echo "skill.nix: ai/SKILL.md has no 'name: scruff' line to rewrite" >&2
      exit 1
    }
    for dir in "$ai"/*/; do
      [ -f "$dir/SKILL.md" ] || continue
      name="$(basename "$dir")"
      mkdir -p "$out/$name"
      cp "$dir/SKILL.md" "$out/$name/SKILL.md"
    done

    # The guards live in script/check-skills.sh, not here, and that is the whole
    # point: scruff's CI installs Go and bats and no Nix, so a guard written into
    # this derivation would run on a developer's machine and nowhere else. Both
    # callers run the same copy, over the same discovered set.
    bash ${../script/check-skills.sh} "$ai" scruff
  ''
