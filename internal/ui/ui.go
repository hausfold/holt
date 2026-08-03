// Package ui is holt's only writer of human-facing text.
//
// The one hard rule, and it is a contract rather than a style choice
// (SPEC.md §2.3): stdout carries DATA only — the new checkout path from
// `create`/`child`, the JSON from `--json`. Every diagnostic, prompt and
// progress line goes to stderr, because callers do `cd "$(holt child …)"` and
// Claude Code's WorktreeCreate hook reads the path off stdout.
package ui

import (
	"fmt"
	"os"
)

// Colours match the bash `wt` so the two are indistinguishable during the
// dual-run week of the cutover (SPEC.md §10).
const (
	colSay  = "\033[38;5;103m"
	colDie  = "\033[38;5;167m"
	colWarn = "\033[38;5;179m"
	colOff  = "\033[0m"
)

// NoColor is set when stderr isn't a terminal or NO_COLOR is present.
var NoColor = os.Getenv("NO_COLOR") != "" || !IsTTY(os.Stderr)

func paint(col, glyph, msg string) string {
	if NoColor {
		return fmt.Sprintf("%s  %s\n", glyph, msg)
	}
	return fmt.Sprintf("%s%s  %s%s\n", col, glyph, msg, colOff)
}

// Say prints an informational line to stderr.
func Say(format string, a ...any) {
	fmt.Fprint(os.Stderr, paint(colSay, "🌫", fmt.Sprintf(format, a...)))
}

// Warn prints a caution line to stderr.
func Warn(format string, a ...any) {
	fmt.Fprint(os.Stderr, paint(colWarn, "!", fmt.Sprintf(format, a...)))
}

// Fail prints an error line to stderr. It does not exit — main owns that, so
// that every path returns an error carrying its exit code.
func Fail(msg string) {
	fmt.Fprint(os.Stderr, paint(colDie, "✗", msg))
}

// Out prints to stdout. Reserve it for data.
func Out(format string, a ...any) {
	fmt.Fprintf(os.Stdout, format, a...)
}

// IsTTY reports whether f is a terminal. Callers use it to decide between
// exec-ing an interactive client and printing the command to run instead.
func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
