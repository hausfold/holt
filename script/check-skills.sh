#!/usr/bin/env bash
# The guards on holt's agent skills — one copy, run from two places.
#
# Every failure here is INVISIBLE at runtime. A skill whose frontmatter is
# missing, unterminated, or whose `name:` disagrees with its directory installs
# fine, lists fine, and is never loaded — indistinguishable, from the user's
# side, from the agent not knowing holt exists. So it has to be a build failure,
# and it has to fire in CI.
#
# Which is why this is a script and not a `runCommand` body: holt's CI installs
# Go and bats and no Nix at all, so guards living only in nix/skill.nix would
# run on a developer's machine and nowhere else. nix/skill.nix calls this, and
# `check.yml` calls it directly.
#
# Usage: script/check-skills.sh <name> <path> [<name> <path> …]
set -euo pipefail

status=0
bad() { printf '%s\n' "$*" >&2; status=1; }

[ "$#" -ge 2 ] && [ $(( $# % 2 )) -eq 0 ] || {
  printf 'usage: check-skills.sh <name> <path> [<name> <path> ...]\n' >&2
  exit 2
}

while [ "$#" -ge 2 ]; do
  name="$1" skill="$2"; shift 2

  [ -f "$skill" ] || { bad "$name: no SKILL.md at $skill"; continue; }

  # The frontmatter, and ONLY the frontmatter. Every client routes on `name` and
  # `description`; keys that appear further down the body are prose.
  if ! head -1 "$skill" | grep -qx -- '---'; then
    bad "$name: SKILL.md does not open with YAML frontmatter"
    continue
  fi
  front="$(tail -n +2 "$skill" | sed -n '1,/^---$/p')"
  printf '%s\n' "$front" | grep -qx -- '---' \
    || { bad "$name: SKILL.md frontmatter block is never closed"; continue; }

  # The directory name and the `name:` key are two identifiers for one skill —
  # the path a client scans, and the string it routes on. A mismatch installs a
  # skill under a name nothing ever asks for.
  printf '%s\n' "$front" | grep -qx "name: $name" \
    || bad "$name: SKILL.md has no 'name: $name' line"

  # One PHYSICAL line, by design: these guards are grep, and a description
  # written as a YAML folded scalar (`>-` plus an indented body) is valid YAML
  # that would silently stop being checked. The family standard says one line.
  printf '%s\n' "$front" | grep -qE '^description: .{80,}' \
    || bad "$name: SKILL.md description is missing, too short to route on, or wrapped onto a second line"

  # A routing document that grew into a manual stops being read as one.
  lines=$(wc -l < "$skill")
  [ "$lines" -le 150 ] \
    || bad "$name: SKILL.md is $lines lines; the standard caps a skill at 150"
done

exit "$status"
