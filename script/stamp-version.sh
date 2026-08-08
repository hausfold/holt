#!/usr/bin/env bash
# One version number, written into every place holt declares one.
#
# holt ships SIX artifacts out of one repo — the CLI plus five SDKs — and until
# this script existed each carried its own hand-typed string. Four of them are
# already live on third-party registries at 0.1.0 (npm, PyPI, crates.io, and the
# SwiftPM mirror), and npm/PyPI/crates versions are IMMUTABLE: a number published
# once can never be re-cut, only superseded. That is why holt is semver rather
# than the family's usual CalVer — the number is a compatibility contract read by
# consumers of a public library, not just a date, and the four registries already
# hold semver.
#
# Two of the six carry no version string at all and so aren't listed below:
#
#   sdk/go     Go modules take their version from a git tag and nothing else.
#              `sdk/go` is its own module (its own go.mod, so `go get` doesn't
#              drag in holt's deps), so the tag is PREFIXED with the path:
#              `sdk/go/v<version>`. release.yml pushes it.
#   sdk/swift  SwiftPM needs Package.swift at a repo root, so sdk/swift is
#              mirrored to nebelhaus/holt-swift and the version is a tag on the
#              MIRROR. sync-mirror.sh pushes it; release.yml calls that.
#
# Usage:
#   script/stamp-version.sh 0.2.0            write it everywhere
#   script/stamp-version.sh --check 0.2.0    verify everywhere already says it
#
# `bench release holt <version>` calls the write form; release.yml calls the
# check form against the pushed tag, so a tag can never claim a version the
# code doesn't.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

check=""
if [ "${1:-}" = "--check" ]; then check=1; shift; fi
version="${1:-}"

if [ -z "$version" ]; then
  echo "usage: script/stamp-version.sh [--check] <version>" >&2
  exit 2
fi

# Strict X.Y.Z, no prerelease suffix. Not pedantry: the five ecosystems only
# agree on the plain triple. PEP 440 would silently rewrite `0.2.0-rc1` to
# `0.2.0rc1` on the Python side while npm and crates keep it verbatim, so the
# one number would stop being one number. If holt ever wants prereleases they
# need per-ecosystem spellings, and that belongs here, deliberately — not as an
# accident of a loose regex.
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "stamp-version: '$version' is not a plain X.Y.Z semver" >&2
  echo "  (holt releases have no prerelease suffix — see the comment in this script)" >&2
  exit 2
fi

# Each entry: <path><TAB><sed match><TAB><sed replacement>. The match is
# anchored hard enough that no neighbouring key can collide with it — notably
# Cargo.toml's `rust-version` and pyproject's `python_version`, both of which a
# lazy /version = / would eat.
stamp() { # stamp <relpath> <sed-expression>
  local f="$root/$1" expr="$2" tmp
  [ -f "$f" ] || { echo "stamp-version: missing $1" >&2; exit 1; }
  # Redirect + mv rather than `sed -i`: GNU and BSD sed disagree about whether
  # -i takes a suffix argument, and this runs on both (dev macOS, CI Linux).
  tmp="$(mktemp)"
  sed -E "$expr" "$f" >"$tmp"
  mv "$tmp" "$f"
}

read_field() { # read_field <relpath> <sed-print-expression>
  sed -nE "$2" "$root/$1" | head -1
}

TS_READ='s/^  "version": "([^"]*)".*/\1/p'
PY_READ='s/^version = "([^"]*)".*/\1/p'
RS_READ='s/^version = "([^"]*)".*/\1/p'

if [ -n "$check" ]; then
  fail=0
  report() { # report <label> <found>
    if [ "$2" = "$version" ]; then
      printf '  ok   %-26s %s\n' "$1" "$2"
    else
      printf '  BAD  %-26s %s (want %s)\n' "$1" "${2:-<unset>}" "$version"
      fail=1
    fi
  }
  report VERSION                  "$(tr -d '[:space:]' <"$root/VERSION")"
  report sdk/ts/package.json      "$(read_field sdk/ts/package.json "$TS_READ")"
  report sdk/python/pyproject.toml "$(read_field sdk/python/pyproject.toml "$PY_READ")"
  report sdk/rust/Cargo.toml      "$(read_field sdk/rust/Cargo.toml "$RS_READ")"
  [ "$fail" = 0 ] || {
    echo "stamp-version: the tree disagrees with $version — run script/stamp-version.sh $version" >&2
    exit 1
  }
  echo "stamp-version: everything says $version"
  exit 0
fi

printf '%s\n' "$version" >"$root/VERSION"
stamp sdk/ts/package.json        "s/^(  \"version\": \")[^\"]*(\")/\1$version\2/"
stamp sdk/python/pyproject.toml  "s/^(version = \")[^\"]*(\")/\1$version\2/"
stamp sdk/rust/Cargo.toml        "s/^(version = \")[^\"]*(\")/\1$version\2/"

# Never trust the write — re-read. A regex that quietly matched nothing leaves a
# manifest on the old number, and the first thing that would notice is an
# immutable, wrong publish.
exec "${BASH_SOURCE[0]}" --check "$version"
