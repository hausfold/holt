# holt (Go SDK)

A thin Go client over the [`holt`](../../README.md) binary — the
worktree-lifecycle substrate for parallel coding agents. It shells out to
`holt` (`os/exec` + `--json`, `watch --json` for a live NDJSON stream)
rather than talking to a daemon.

This directory is its own Go module (`github.com/nebelhaus/holt/sdk/go`),
separate from the CLI's at the repo root, so importing it doesn't pull in
holt's own dependencies (fsnotify, the CLI framework) — only the standard
library.

## Install

```sh
go get github.com/nebelhaus/holt/sdk/go
```

Versions come from pushed tags shaped `sdk/go/vX.Y.Z`. None exist yet —
for now, use `go get github.com/nebelhaus/holt/sdk/go@<commit-sha>` or a
`replace` directive pointing at a local checkout.

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
	// that as false.
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
does — so piped stdio blocks forever. `Resume` (non-interactive) is safe
from a server: holt detects piped stdout and prints the reopen command as
text instead of exec'ing.

## Holding a session open: leases

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
"nobody leased it" isn't proof nobody's there.

`Lease` fires its first heartbeat in the background instead of blocking
the constructor on it; a failed take surfaces on the next refresh. Call
`c.Heartbeat(ctx, path, 0)` yourself first if you need the initial take
to be synchronous and its error immediate.

## Errors

Every method that shells out returns `*holt.Error` on a non-zero exit,
carrying holt's real exit code:

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

## Not covered

`hook create`/`hook remove` (the Claude Code hook protocol) have no
wrapper — shell out via `exec.Command` yourself if you need them. Types
are hand-ported from the Go structs in `internal/commands/`, not shared
by import, so this module has no dependency on the CLI module.

## Testing

`testdata/fake-holt.sh` stands in for the real binary (shared with
`sdk/ts` and `sdk/python`'s fixture of the same name).

```sh
go test ./...
go vet ./...
```
