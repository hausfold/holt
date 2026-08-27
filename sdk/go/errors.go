package scruff

import (
	"fmt"
	"strings"
)

// Error is returned by every SDK call that shells out and gets back a
// non-zero exit. It carries scruff's actual exit code (SPEC.md §2.4) rather
// than collapsing every failure into one shape — Refused() is how a caller
// tells "scruff declined to destroy something" from "you asked wrong"
// (ExitUsage) or "registry locked" (ExitLocked), and each deserves
// different handling (retry, surface to a human, or just don't retry).
//
// Named Error, not ScruffError, to match this package's own qualifier
// (scruff.Error) — the same convention internal/exitcode.Error uses inside
// scruff itself.
type Error struct {
	Code    ExitCode
	Stderr  string
	Command []string
}

func (e *Error) Error() string {
	label := exitLabel(e.Code)
	if e.Stderr != "" {
		return fmt.Sprintf("scruff %s: %s — %s", strings.Join(e.Command, " "), label, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("scruff %s: %s", strings.Join(e.Command, " "), label)
}

// Refused reports whether scruff declined for safety (occupied, dirty, or
// not provably landed) rather than because the call itself was wrong.
func (e *Error) Refused() bool { return e.Code == ExitRefused }

// Degraded reports whether the operation completed but a signal was
// unavailable (forge down, no lsof) — check an Envelope's Warnings for why.
func (e *Error) Degraded() bool { return e.Code == ExitDegraded }

func exitLabel(code ExitCode) string {
	switch code {
	case ExitUsage:
		return "usage"
	case ExitRefused:
		return "refused"
	case ExitDegraded:
		return "degraded"
	case ExitConflict:
		return "conflict"
	case ExitLocked:
		return "locked"
	default:
		return fmt.Sprintf("exit %d", int(code))
	}
}
