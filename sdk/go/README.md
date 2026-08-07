# holt (Go SDK)

A thin Go client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. holt stays a
binary; this SDK shells out to it (`os/exec` + `--json`, `watch --json`
for a live NDJSON stream) rather than talking to a daemon, because there
isn't one (SPEC.md §14.1).

Nested module: this directory has its own `go.mod`
(`github.com/nebelhaus/holt/sdk/go`), separate from the CLI's at the repo
root, so importing it doesn't pull in holt's own dependencies (fsnotify,
the CLI framework) — only the standard library.

## Install

```sh
go get github.com/nebelhaus/holt/sdk/go
```

Go modules resolve straight from this git repo via the module proxy — no
separate publish step, no package-manager account, unlike npm/PyPI. The
only thing that makes a version installable is a pushed tag shaped
`sdk/go/vX.Y.Z` (the `sdk/go/` prefix is what tells the Go tooling this
tag versions the nested module, not the root one). None exist yet — for
now, `go get github.com/nebelhaus/holt/sdk/go@<commit-sha>` or a `replace`
directive pointing at a local checkout.

`holt` itself must be on `PATH`, or set `Client.Bin` to its path.

## Two shapes of usage

**Programmatic (a web backend, an orchestrator).** Every `Client` method
except the two ending in `Interactive` captures the child's stdout and
returns — safe to call from a server with many concurrent sessions. The
zero value is a complete client:

```go
import (
	"context"
	holt "github.com/nebelhaus/holt/sdk/go"
)

c := &holt.Client{}
ctx := context.Background()

envelope, err := c.List(ctx)
for _, lane := range envelope.Lanes {
	// Occupied/Dirty are *bool — nil means "not determined", never treat
	// that as false (SPEC.md §2.2's whole nullable-discipline point).
	fmt.Println(lane.Name, lane.State, lane.Occupied)
}

// Create a lane WITHOUT attaching an agent to it — the primitive an
// orchestrator wants. Child/Spawn only ever print the new path.
dir, err := c.Child(ctx, "/path/to/some-repo", "task-42")
// ...now launch YOUR OWN agent process against dir.
```

```go
// Live updates instead of polling — created/parked/resumed/reaped/changed.
// Go 1.23's range-over-func makes this a plain for-range, no channel or
// callback plumbing needed: breaking out of the loop (or canceling ctx)
// kills the underlying `holt watch` process.
for line, err := range c.Watch(ctx) {
	if err != nil {
		log.Println("watch:", err)
		break
	}
	if line.Kind == holt.WatchCreated {
		notifyUI(line.Lane)
	}
}
```

**Interactive (a real terminal TUI).** `NewInteractive` / `ResumeInteractive`
inherit the calling process's stdio, so when holt execs the configured
agent client (`claude`, `codex`, `opencode`), it takes over the real
terminal — same as running `holt new` by hand — and control returns to
you when that session ends.

```go
// A Go TUI, run in an actual terminal:
if err := c.NewInteractive(ctx, "task-42", ""); err != nil {
	log.Fatal(err)
}
// ... the agent owned the screen; you're back here when it exits.
```

**Do not call `NewInteractive` from a server.** `holt new` execs the agent
client unconditionally — it doesn't check for a TTY the way `resume`
does — so calling it with piped stdio blocks forever with your pipes
attached to whatever the agent expects on stdin. `Resume` (the
non-interactive form) is safe from a server: holt detects the piped
stdout and prints the reopen command as text instead of exec'ing.

## Holding a session open: leases, not callbacks

holt's sweep (`reap`) needs to know a checkout is in use. On a human's
machine, `lsof` answers that. A server holding one session per lane has
no pane and no shell cwd'd anywhere — so it says so itself, with a lease:

```go
lease := c.Lease(ctx, laneDir, holt.LeaseOptions{}) // refreshes on an interval, < the 90s TTL
// ... serve the session ...
lease.Release(context.Background())
```

Pass `LeaseOptions{PID: pid}` instead when the lease should track a real
local process — the OS then drops it the instant that pid dies, no
refresh loop needed.

A lease can only **save** a lane from `reap`, never condemn one —
"nobody leased it" isn't proof nobody's there. See SPEC.md §14.2.

`Lease` fires its first heartbeat in the background rather than blocking
the constructor on it (a constructor has nowhere clean to return an
error) — a failed take surfaces on the next refresh, the same
best-effort/self-healing behavior every later refresh already has. Call
`c.Heartbeat(ctx, path, 0)` yourself first if you need the initial take
to be synchronous and its error immediate.

## Errors

Every method that shells out returns `*holt.Error` on a non-zero exit,
carrying holt's real exit code (SPEC.md §2.4) rather than collapsing
every failure into one shape:

```go
_, err := c.Unpark(ctx)
var herr *holt.Error
if errors.As(err, &herr) && herr.Refused() {
	// holt declined for safety (occupied, dirty, already pushed) —
	// different handling than a plain usage error.
}
```

`Refused()` is "holt declined to destroy something"; `Degraded()` is
"completed, but a signal was unavailable (forge down, no `lsof`)" — check
an `Envelope`'s `Warnings` for why.

## What's NOT here yet

- `hook create`/`hook remove` (the Claude Code hook protocol, SPEC.md
  §2.3) have no wrapper — they're for editor integrations, not the
  orchestrator use case this SDK targets first. Shell out via
  `exec.Command` yourself if you need them.
- The `--json` envelope's future fields (`pr`, `overlap`, `ahead`/
  `behind` — SPEC.md §2.2's example, gated behind the `overlap`/forge-
  polling milestones) aren't in `Lane` because they aren't on the wire in
  schema 1 yet. Don't add them here before `internal/commands/json.go`
  does.
- `holt.agentInstructions()` (SPEC.md §14.5's `holt docs agent --format=json`)
  — the TS SDK doesn't have a wrapper for it yet either; add one here once
  that lands.
- Types are hand-ported from the Go structs in `internal/commands/`, not
  shared by import — this module deliberately has no dependency on the
  CLI module, so it can't just reuse `jsonEnvelope`/`jsonLane` directly.
  If holt's JSON shape and this file drift, that's a real bug class this
  SDK exists to avoid — SPEC.md §14.1 says "generate SDK types from it"
  as the intended end state. A `go generate` step emitting this file (or
  a small shared internal package the CLI and this SDK both import) is
  the natural fix; out of scope for this first pass.

## Testing

`testdata/fake-holt.sh` stands in for the real binary so tests don't need
a Go build of `holt` itself — it's a fixture, not a spec of holt's
behavior, and is shared verbatim with `sdk/ts` and `sdk/python`'s fixture
of the same name. Once `holt` builds in CI, add a second suite that runs
the same assertions against the real binary in a scratch repo.

```sh
go test ./...
go vet ./...
```
