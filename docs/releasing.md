# Releasing holt

holt ships **six artifacts out of one repository** — the CLI plus five SDKs — and
they all carry the same version number. One tag publishes all of them.

| artifact | published as | how |
|---|---|---|
| CLI | the GitHub release + source tarball | Nix consumers take the flake input; there is no binary to attach |
| `sdk/ts` | npm `@hausfold/holt` | `npm publish` over OIDC |
| `sdk/python` | PyPI `hausfold-holt` | `gh-action-pypi-publish` over OIDC |
| `sdk/rust` | crates.io `hausfold-holt` | `cargo publish` over OIDC |
| `sdk/go` | `github.com/hausfold/holt/sdk/go` | a `sdk/go/v<version>` tag — Go's proxy needs nothing else |
| `sdk/swift` | `github.com/hausfold/holt-swift` | a `<version>` tag on the mirror — SwiftPM likewise |

## Cutting one

```sh
bench release holt 0.2.0
```

That is the whole flow. It stamps the version into every manifest, commits it,
pushes, tags `v0.2.0`, and then blocks — painting the CI job tree live — until
every publish job has finished. It exits non-zero if any of them goes red.

Never push a `v*` tag by hand. The `version stamp` job in
[`.github/workflows/release.yml`](../.github/workflows/release.yml) re-checks
every manifest against the tag and fails the run if they disagree, so a
hand-pushed tag just wastes a run.

## Why semver, when the rest of the family is CalVer

Because three of these registries are immutable and already hold `0.1.0`. npm,
PyPI and crates.io never let a published version be withdrawn or re-cut — only
superseded — so the number is a compatibility contract read by people who pinned
against it, not a date. CalVer would also force the Go SDK's import path to end
in `/v2026` (Go's major-version rule) and change it every January.

Versions are a plain `X.Y.Z` with no prerelease suffix. That is the only spelling
all five ecosystems agree on: PEP 440 would silently rewrite `0.2.0-rc1` to
`0.2.0rc1` on the Python side while npm and crates kept it verbatim, and the one
number would stop being one number.

### Picking the bump

Judge the diff against the **published SDK surface**, not the CLI internals:

```sh
LAST=$(git describe --tags --abbrev=0 --match 'v*')
git diff "$LAST"..main -- sdk/
```

- **major** — something existing SDK code depends on changed or disappeared: an
  exported signature, a returned shape, a field on an event.
- **minor** — new capability; everything that compiled before still compiles.
- **patch** — a fix, a doc, a test, or a CLI-only change that moves no SDK surface.

Two rules override the taxonomy:

- **All five SDKs share the one number.** A breaking change in the Rust client
  alone bumps all five. Five clients agreeing about one wire format is the
  invariant the `sdks` CI job exists to protect — five drifting version lines
  would hide a divergence instead of surfacing it.
- **Pre-1.0, a break is still at least a minor.** holt is `0.x`. If it would have
  been major at 1.0, cut `0.1.0` → `0.2.0` and say so in the notes. Never
  smuggle a break into a patch: that is the number people pin against.

## Where the version lives

[`script/stamp-version.sh`](../script/stamp-version.sh) owns this, and it is the
only thing that should write these files:

- `VERSION`
- `sdk/ts/package.json`
- `sdk/python/pyproject.toml`
- `sdk/rust/Cargo.toml`

`sdk/go` and `sdk/swift` declare no version at all — for both, the tag *is* the
release — which is why they aren't in that list.

```sh
script/stamp-version.sh 0.2.0            # write it everywhere
script/stamp-version.sh --check 0.2.0    # what CI runs against the pushed tag
```

## One-time registry setup

Every publish authenticates by OIDC: no long-lived tokens, no 2FA prompt at
release time, and npm and PyPI attach build provenance for free. Each registry
has to be told to trust this repo and workflow **once**, in a browser:

| registry | where | what to enter |
|---|---|---|
| npm | npmjs.com → `@hausfold/holt` → Settings → Trusted Publisher | GitHub Actions, org `hausfold`, repo `holt`, workflow `release.yml` |
| PyPI | pypi.org → `hausfold-holt` → Publishing → Add a trusted publisher | owner `hausfold`, repo `holt`, workflow `release.yml` |
| crates.io | crates.io → `hausfold-holt` → Settings → Trusted Publishing | repo `hausfold/holt`, workflow `release.yml` |

The Swift mirror is the one that can't use OIDC — pushing to *another* repository
is outside what this workflow's `GITHUB_TOKEN` can ever be scoped to. Mint a
fine-grained PAT with `Contents: read and write` on `hausfold/holt-swift` and
store it as the repo secret `MIRROR_TOKEN`.

## When a publish fails

The publish jobs are deliberately **independent and idempotent**. A registry that
rate-limits, or one whose trusted publisher wasn't wired yet, fails alone — the
other five still land. Fix the cause and:

```sh
gh run rerun --failed --repo hausfold/holt <run-id>
```

Each job re-checks whether its version is already out there and no-ops if so, so
a rerun finishes the release rather than half-cutting a second one. **Never
respond to a failed publish by bumping the version** — that burns a number
permanently on the registries that did succeed.

## Afterwards

The version stamp is a commit, so holt's HEAD moved and the rice's `flake.lock`
pin of it is now stale:

```sh
bench ship                          # or: bench release holt 0.2.0 --ship
```
