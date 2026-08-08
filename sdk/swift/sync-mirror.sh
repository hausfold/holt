#!/usr/bin/env bash
# Pushes sdk/swift to the nebelhaus/holt-swift mirror via `git subtree split`,
# since SwiftPM needs Package.swift at a repo's root for a remote git
# dependency (see this dir's README's "Install" section).
#
# Two callers, one code path:
#
#   sdk/swift/sync-mirror.sh              from main, by hand — mirror main only.
#                                         Use it to get an unreleased change in
#                                         front of a consumer pinning a branch.
#   sdk/swift/sync-mirror.sh --tag 0.2.0  what release.yml runs at a v* tag:
#                                         mirror the tree AND tag it, which is
#                                         the whole of "publishing" for SwiftPM
#                                         (there is no registry to push to — a
#                                         tag on the mirror IS the release).
#
# Run from the holt repo root. Auth comes from MIRROR_URL, which CI overrides
# with a credential-bearing URL because the workflow's own GITHUB_TOKEN is
# scoped to THIS repo and cannot push to the mirror.
set -euo pipefail

MIRROR_URL="${MIRROR_URL:-https://github.com/nebelhaus/holt-swift.git}"

tag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --tag) tag="${2:-}"
           [ -n "$tag" ] || { echo "sync-mirror: --tag needs a version" >&2; exit 2; }
           shift 2 ;;
    *) echo "sync-mirror: unknown argument: $1   (usage: sync-mirror.sh [--tag <version>])" >&2; exit 2 ;;
  esac
done

if [[ -n "$(git status --porcelain)" ]]; then
  echo "sync-mirror: working tree is dirty — commit or holt park first" >&2
  exit 1
fi

# The branch guard exists so a human doesn't mirror a feature branch. Tagging a
# release is strictly stronger evidence than "you are on main" — and CI checks
# out a tag, so HEAD is detached there and symbolic-ref would fail outright.
if [[ -z "$tag" ]]; then
  branch="$(git symbolic-ref --short HEAD)"
  if [[ "$branch" != "main" ]]; then
    echo "sync-mirror: run this from main (currently on $branch), or pass --tag <version>" >&2
    exit 1
  fi
fi

tmp="sync-mirror-tmp-$$"
trap 'git branch -D "$tmp" 2>/dev/null || true' EXIT
git subtree split --prefix=sdk/swift -b "$tmp" >/dev/null
git push "$MIRROR_URL" "$tmp:main"
echo "sync-mirror: pushed sdk/swift -> holt-swift main"

if [[ -z "$tag" ]]; then
  echo "sync-mirror: not tagged. A release tags the mirror for you:"
  echo "    bench release holt <version>"
  exit 0
fi

# Idempotent, because a release run gets re-run. Publishing to five ecosystems
# means any one of them can fail on its own (a flaky registry, a secret that
# wasn't set yet) while the others succeeded — and `gh run rerun --failed` has
# to be safe, not a second half-release. An existing tag here is success.
if git ls-remote --tags --exit-code "$MIRROR_URL" "refs/tags/$tag" >/dev/null 2>&1; then
  echo "sync-mirror: holt-swift already tagged $tag — nothing to do"
  exit 0
fi

# Tagged on the split commit, not on holt's own history: the mirror's commits
# are the subtree-split rewrites, and $tmp is exactly that tip.
git tag -f "sync-mirror-tag-$$" "$tmp"
git push "$MIRROR_URL" "refs/tags/sync-mirror-tag-$$:refs/tags/$tag"
git tag -d "sync-mirror-tag-$$" >/dev/null
echo "sync-mirror: tagged holt-swift $tag"
