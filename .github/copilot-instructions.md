# Copilot instructions

**Read [`AGENTS.md`](../AGENTS.md) at the repo root first — it is the full,
authoritative instruction set for every agent working here, and this file is
only a pointer to it.** (Copilot doesn't follow file imports, hence the
duplication below; if the two ever disagree, `AGENTS.md` wins.)

The short version:

- scruff is the **worktree-lifecycle substrate** for parallel coding agents: a Go
  CLI plus five SDKs, published from this repo under **one** version number.
  It is a substrate, not an orchestrator — no scheduling, no agent supervision,
  no TUI, no knowledge of anyone's build system.
- **Three safety invariants outrank everything**, in this order: never lose work
  (destructive paths park first); never reap something in use (uncertainty
  resolves to *keep*); the locked registry is the source of truth, not the
  filesystem. A change that trades one of these for convenience is wrong even
  when the suite is green.
- **`SPEC.md` §2 is frozen** — registry schema, `--json` output, hook protocol,
  exit codes. Downstreams pin them, so changing one is a semver *major*
  conversation, not a refactor.
- **`github.com/hausfold/holt` in a released tag is deliberate.** Go's proxy is
  immutable, so the pre-1.0.0 path stays resolvable forever and is not a missed
  hit from the scruff rename. New code says `github.com/hausfold/scruff`.
- **`sdk/swift` is the source; `hausfold/scruff-swift` is a generated mirror** —
  never propose edits to the mirror.
- Verify the CLI with `make check` (gofmt, vet, `go test ./...`, and the
  black-box bats acceptance suite), on macOS **and** Linux — CI runs both.
  `make check` does **not** cover the SDKs: `sdk/go` has its own `go.mod` and the
  other four have no Make target, so an SDK change is verified by that SDK's own
  suite (`bun test`, `pytest`, `cargo test`, `swift test`) and by CI's separate
  `sdks` / `swift-sdk` jobs.
- **Land through a PR** — never a direct push or a local `git merge` into `main`.

For review comments, the same bar applies as anywhere else here: correctness and
the invariants above over style. If a diff touches a frozen contract or a
lifecycle invariant, say so plainly — that's the review this repo actually needs.
