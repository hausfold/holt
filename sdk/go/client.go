// Package holt is a thin Go client over the holt binary — the worktree-
// lifecycle substrate for parallel coding agents. holt stays a binary;
// this package shells out to it (exec + --json, watch --json for a live
// NDJSON stream) rather than talking to a daemon, because there isn't one
// (SPEC.md §14.1).
package holt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client is a thin client over the holt binary. Every method shells out —
// there is no daemon, no port, no socket (SPEC.md §14.1) — so the zero
// value is a complete, usable client:
//
//	c := &holt.Client{}
//	envelope, err := c.List(ctx)
//
// Two methods (NewInteractive, ResumeInteractive) inherit the calling
// process's stdio and can hand off the terminal to a coding agent; every
// other method captures output and returns. Mixing them up matters — see
// each method's doc comment. Client holds nothing but the options below,
// so it's cheap to copy or construct as often as you like; it is safe for
// concurrent use because every call is a fresh subprocess.
type Client struct {
	// Bin is the path to the holt binary, or a bare name resolved on
	// PATH. Empty means "holt".
	Bin string
	// Dir is the working directory every command runs from — most of
	// holt's commands are cwd-sensitive (new, park, a bare `holt
	// <name>`). Empty means this process's own cwd.
	Dir string
	// Env is extra environment variables, merged over (and overriding)
	// the current process's environment — useful for HOLT_AGENT,
	// HOLT_OCCUPANCY=lease. Unlike the TS/Python SDKs, there is no
	// "unset a var" sentinel here: os/exec has none either, so an entry
	// simply adds or overrides, never removes, a parent env var.
	Env map[string]string
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "holt"
	}
	return c.Bin
}

func (c *Client) env() []string {
	if len(c.Env) == 0 {
		return nil // nil means "inherit the parent process's environment" to os/exec
	}
	env := os.Environ()
	for k, v := range c.Env {
		env = append(env, k+"="+v) // a later entry wins on a duplicate key (os/exec's own rule)
	}
	return env
}

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = c.Dir
	cmd.Env = c.env()
	return cmd
}

// run executes one holt invocation to completion and collects its output.
// Every non---json holt command writes human text to stdout on success —
// this is the primitive List/Watch build their typed parsing on top of,
// and the one lifecycle methods (Park, Reap, ...) use directly, surfacing
// stdout as a plain string.
//
// Returns *Error on a non-zero exit, carrying holt's exit code (SPEC.md
// §2.4) rather than collapsing every failure into one shape.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	cmd := c.command(ctx, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = nil // no stdin: closed immediately, same as the other SDKs

	runErr := cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()
	if runErr == nil {
		return stdout, stderr, nil
	}

	code := ExitUsage // a spawn failure (bad Bin, not a git repo yet) has no real exit code — Usage's bucket
	var exitErr *exec.ExitError
	if ok := asExitError(runErr, &exitErr); ok {
		code = ExitCode(exitErr.ExitCode())
	}
	return stdout, stderr, &Error{Code: code, Stderr: stderr, Command: append([]string{c.bin()}, args...)}
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func (c *Client) runJSON(ctx context.Context, v any, args ...string) error {
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(stdout), v)
}

// List runs `holt --json` / `holt list --json` — byte-identical (SPEC.md
// §2.2). The full snapshot: every live/parked lane, across every repo
// holt knows about. Poll this for landedness and PR state; use Watch for
// everything else, since it's push rather than poll.
func (c *Client) List(ctx context.Context) (*Envelope, error) {
	var env Envelope
	if err := c.runJSON(ctx, &env, "--json"); err != nil {
		return nil, err
	}
	return &env, nil
}

// Child runs `holt child <repo> [name]` — a lane on ANOTHER repo,
// registered as a child of Dir. Prints only the new checkout's path on
// stdout (SPEC.md §2.3's "only the path" discipline extends here too) and
// never execs a client, which is what makes it the right primitive for an
// orchestrator: create the lane, then run your OWN agent process against
// the path it returns. name == "" omits the argument.
func (c *Client) Child(ctx context.Context, repoPath, name string) (string, error) {
	args := []string{"child", repoPath}
	if name != "" {
		args = append(args, name)
	}
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// Spawn runs `holt spawn <repo> <name> [agent]` — a named lane for a
// caller with no pane of its own (a scheduler, a web backend). Like
// Child, only ever creates the lane and prints its path; never execs.
// agent == "" omits the argument.
func (c *Client) Spawn(ctx context.Context, repoPath, name, agent string) (string, error) {
	args := []string{"spawn", repoPath, name}
	if agent != "" {
		args = append(args, agent)
	}
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// Resume runs `holt <name>` / `holt resume <name>` with stdout captured
// rather than a terminal — which means the Go binary's own TTY check
// (ui.IsTTY) sees a pipe and, by design, never execs a client. It
// rebuilds the checkout if needed and returns the human-readable result:
// either confirmation it's ready, or the exact command to reopen the
// agent's chat by hand. Safe to call from a server process. For a TUI
// that wants to actually hand off the terminal, use ResumeInteractive
// instead.
func (c *Client) Resume(ctx context.Context, name string) (string, error) {
	stdout, _, err := c.run(ctx, "resume", name)
	return stdout, err
}

// Park runs `holt park [label]` — commits the working tree as one
// `wip:` commit on the current branch. Never touches the shared stash
// stack (README's "park, not git stash" section) — this is the one safe
// way for concurrent lanes to set work aside. label == "" omits the
// argument.
func (c *Client) Park(ctx context.Context, label string) error {
	args := []string{"park"}
	if label != "" {
		args = append(args, label)
	}
	_, _, err := c.run(ctx, args...)
	return err
}

// Unpark runs `holt unpark` — reverses the most recent Park, putting its
// changes back uncommitted. Returns an *Error with Refused() true if that
// commit is already pushed (holt will not rewrite published history) or
// HEAD isn't a parked commit.
func (c *Client) Unpark(ctx context.Context) error {
	_, _, err := c.run(ctx, "unpark")
	return err
}

// Reap runs `holt reap` — sweeps every LANDED lane nobody is standing in
// (occupied, per Heartbeat/lsof, always wins). Never removes the checkout
// holt is being run from, and never removes a stray.
func (c *Client) Reap(ctx context.Context) error {
	_, _, err := c.run(ctx, "reap")
	return err
}

// Reship runs `holt reship [name]` — pushes a branch that outran its
// already-merged PR, and opens the follow-up. Returns an *Error with
// Degraded() true if `gh` itself is unavailable. name == "" omits the
// argument.
func (c *Client) Reship(ctx context.Context, name string) error {
	args := []string{"reship"}
	if name != "" {
		args = append(args, name)
	}
	_, _, err := c.run(ctx, args...)
	return err
}

// Heartbeat runs `holt heartbeat [path] [--pid N]` — takes or refreshes
// the occupancy lease on a checkout (SPEC.md §9.1, §14.2). This is the
// seam built for exactly this SDK: a program embedding holt has no pane
// and no shell cwd'd anywhere, so the lease is the only way Reap learns a
// checkout is in use. A lease can only SAVE a lane from the sweep, never
// condemn one — see Lease for a self-refreshing wrapper instead of
// calling this on a timer yourself. path == "" uses Dir; pid == 0 omits
// --pid (0 is never a real pid).
func (c *Client) Heartbeat(ctx context.Context, path string, pid int) error {
	args := []string{"heartbeat"}
	if path != "" {
		args = append(args, path)
	}
	if pid != 0 {
		args = append(args, "--pid", fmt.Sprintf("%d", pid))
	}
	_, _, err := c.run(ctx, args...)
	return err
}

// ReleaseHeartbeat drops the lease taken by Heartbeat.
func (c *Client) ReleaseHeartbeat(ctx context.Context, path string) error {
	args := []string{"heartbeat"}
	if path != "" {
		args = append(args, path)
	}
	args = append(args, "--release")
	_, _, err := c.run(ctx, args...)
	return err
}

// NewInteractive runs `holt new [name] --open [agent]` with stdio INHERITED from
// the calling process. holt execs the configured agent client
// unconditionally here (unlike Resume, `new` doesn't check for a TTY) —
// appropriate for a real terminal app (a TUI) that wants to hand off the
// screen and get control back when the agent session ends, and WRONG for
// a server: it will block until the agent process exits, with your
// stdio attached to whatever the agent expects. name == "" and agent ==
// "" omit their arguments.
func (c *Client) NewInteractive(ctx context.Context, name, agent string) error {
	args := []string{"new"}
	if name != "" {
		args = append(args, name)
	}
	// --open is explicit: bare `holt new` only prints the lane's path, and
	// this method's whole contract is "become the agent session".
	args = append(args, "--open")
	if agent != "" {
		args = append(args, agent)
	}
	return c.runInteractive(ctx, args...)
}

// ResumeInteractive runs `holt resume <name>` / `holt <name>` with stdio
// INHERITED, so a real terminal's TTY check passes and holt hands off
// the screen to the agent client. Same caveat as NewInteractive: blocks
// until that session ends.
func (c *Client) ResumeInteractive(ctx context.Context, name string) error {
	return c.runInteractive(ctx, "resume", name)
}

func (c *Client) runInteractive(ctx context.Context, args ...string) error {
	cmd := c.command(ctx, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	code := ExitUsage
	var exitErr *exec.ExitError
	if asExitError(runErr, &exitErr) {
		code = ExitCode(exitErr.ExitCode())
	}
	return &Error{Code: code, Command: append([]string{c.bin()}, args...)}
}
