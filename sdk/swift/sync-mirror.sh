#!/usr/bin/env bash
# Pushes sdk/swift's current main to the nebelhaus/holt-swift mirror via
# `git subtree split`, since SwiftPM needs Package.swift at a repo's root
# for a remote git dependency (see this dir's README's "Install" section).
# Run from the holt repo root, after `sdk/swift` changes have landed on
# main. Does NOT tag the mirror — cut that by hand once you know the
# version, same as any other release.
set -euo pipefail

MIRROR_URL="https://github.com/nebelhaus/holt-swift.git"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "sync-mirror: working tree is dirty — commit or stash first" >&2
  exit 1
fi

branch="$(git symbolic-ref --short HEAD)"
if [[ "$branch" != "main" ]]; then
  echo "sync-mirror: run this from main (currently on $branch)" >&2
  exit 1
fi

tmp="sync-mirror-tmp-$$"
trap 'git branch -D "$tmp" 2>/dev/null || true' EXIT
git subtree split --prefix=sdk/swift -b "$tmp"
git push "$MIRROR_URL" "$tmp:main"

echo "pushed sdk/swift -> $MIRROR_URL main. Tag a release there when ready:"
echo "    git clone $MIRROR_URL /tmp/holt-swift-tag && cd /tmp/holt-swift-tag && git tag -a X.Y.Z -m X.Y.Z && git push origin X.Y.Z"
