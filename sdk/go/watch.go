package holt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os/exec"
)

// Watch runs `holt watch --json` as a range-over-func iterator of typed
// lines: a hello, then a sync burst for every lane already alive, ready,
// then live changes for as long as you keep ranging (SPEC.md §14.3 step
// 2). This is the primitive onOpen/onParked/... callback-style APIs are
// built from (SPEC.md §14.2) — see WatchLane for a version scoped to one
// lane's Path.
//
//	for line, err := range c.Watch(ctx) {
//	    if err != nil { ...; break }
//	    if line.Kind == holt.WatchCreated { fmt.Println("new lane:", line.Lane.Name) }
//	}
//
// Unlike the TS/Python SDKs, there is no separate "stop iterating" verb:
// breaking out of the range loop, or canceling ctx, both kill the
// underlying process. Because the iterator body runs synchronously on the
// caller's own goroutine (Go 1.23's iter.Seq2 contract), this needs no
// channel or goroutine bridging to get an async-generator-shaped API —
// the scanner loop below IS the generator.
func (c *Client) Watch(ctx context.Context) iter.Seq2[WatchLine, error] {
	return func(yield func(WatchLine, error) bool) {
		cmd := c.command(ctx, "watch", "--json")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			yield(WatchLine{}, err)
			return
		}
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		cmd.Stdin = nil

		if err := cmd.Start(); err != nil {
			yield(WatchLine{}, err)
			return
		}
		defer func() {
			_ = cmd.Process.Kill() // no-op if already exited
			_ = cmd.Wait()
		}()

		scanner := bufio.NewScanner(stdout)
		// A lane can carry a full branch/path/verdict record; give lines
		// more headroom than bufio's 64KiB default before it errors out.
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var wl WatchLine
			if err := json.Unmarshal(line, &wl); err != nil {
				if !yield(WatchLine{}, err) {
					return
				}
				continue
			}
			if !yield(wl, nil) {
				return
			}
		}

		if ctx.Err() != nil {
			return // caller canceled ctx — that's why the stream ended, not a failure to report
		}
		if err := scanner.Err(); err != nil {
			yield(WatchLine{}, err)
			return
		}
		if err := cmd.Wait(); err != nil {
			code := ExitUsage
			var exitErr *exec.ExitError
			if asExitError(err, &exitErr) {
				code = ExitCode(exitErr.ExitCode())
			}
			yield(WatchLine{}, &Error{Code: code, Stderr: stderrBuf.String(), Command: []string{c.bin(), "watch", "--json"}})
		}
	}
}

// WatchLane is Watch, filtered to events about one lane (Lane.Path) and
// stripped of hello/ready/sync framing — the shape an embedder holding
// one session per lane usually wants: "tell me when THIS lane's state
// changes." Compare full paths, not names: names aren't unique across
// repos, but a checkout path is the registry's own primary key (SPEC.md
// §2.1).
func (c *Client) WatchLane(ctx context.Context, path string) iter.Seq2[WatchLine, error] {
	return func(yield func(WatchLine, error) bool) {
		for line, err := range c.Watch(ctx) {
			if err != nil {
				if !yield(line, err) {
					return
				}
				continue
			}
			if line.Kind == WatchHello || line.Kind == WatchReady {
				continue
			}
			if line.Lane != nil && line.Lane.Path == path {
				if !yield(line, nil) {
					return
				}
			}
		}
	}
}
