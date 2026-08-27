// Package ui is scruff's only writer of human-facing text.
//
// It is a thin adapter over [snug], which owns the palette, the glyphs and the
// folding. scruff names ROLES, never colours: the three xterm-256 indices this
// file used to carry were copied out of the bash `wt` during the cutover, and
// measured against nebelung they sat ΔE 21.8 / 22.3 / 27.4 from the tokens they
// were meant to be — with `say` resolving to blue, the one hue nebelung exists
// to strip out. snug resolves each role against the real palette and degrades it
// by terminal capability, so there is nothing here left to get wrong.
//
// The one hard rule survives the move unchanged, and it is a contract rather
// than a style choice (SPEC.md §2.3): stdout carries DATA only — the new
// checkout path from `create`/`child`, the JSON from `--json`. Every diagnostic,
// prompt and progress line goes to stderr, because callers do
// `cd "$(scruff child …)"` and Claude Code's WorktreeCreate hook reads the path off
// stdout. snug's Printer holds the same contract from its side: Say/Warn/Fail
// write to Err, and Data is the only thing that reaches Out.
package ui

import (
	"os"

	"github.com/hausfold/snug"
)

// printer is scruff's voice, taken once at startup as snug intends — the terminal
// is measured there, not per line.
var printer = snug.NewPrinter()

// Say prints an informational line to stderr.
func Say(format string, a ...any) { printer.Say(format, a...) }

// Warn prints a caution line to stderr.
func Warn(format string, a ...any) { printer.Warn(format, a...) }

// Fail prints an error line to stderr. It does not exit — main owns that, so
// that every path returns an error carrying its exit code.
//
// It takes a message rather than a format, because every caller already has one
// built; the "%s" is what keeps a `%` inside a branch name from being read as a
// verb.
func Fail(msg string) { printer.Fail("%s", msg) }

// Out prints to stdout. Reserve it for data.
func Out(format string, a ...any) { printer.Data(format, a...) }

// IsTTY reports whether f is a terminal. Callers use it to decide between
// exec-ing an interactive client and printing the command to run instead.
func IsTTY(f *os.File) bool { return snug.DetectTerm(f).IsTTY }
