// Package compat carries holt's old name through the rename to scruff.
//
// Everything here exists so that ONE release speaks both names at once. That is
// the safety property of docs/rename.md §2: haus takes this tool as a flake
// input and calls it by name, so if the old spelling stopped answering before
// haus stopped asking, haus would stop evaluating — and the rebuild that would
// fix that is the thing that broke. A bilingual release removes the ordering
// requirement entirely: the binary and its consumer can move in either order,
// or years apart, and never disagree.
//
// The rule the whole package implements: the tool learns the new name before
// anything asks it the new name, and forgets the old name only after nothing
// asks it the old one.
//
// ⚠️ Every symbol here is scheduled for DELETION at 1.1.0 (docs/rename.md §8.1).
// Nothing should be added to it, and no caller belongs here that isn't a rename
// fallback. When this package goes, the grep that proves it is `HOLT_`.
package compat

import (
	"os"
	"path/filepath"
)

// The two spellings, in the order every ladder in this package reads them.
const (
	Prefix    = "SCRUFF_"
	OldPrefix = "HOLT_"

	// Name and OldName are the binary's own two names — the argv[0] the
	// deprecation notice keys off, and the one it points at.
	Name    = "scruff"
	OldName = "holt"
)

// Getenv reads SCRUFF_<suffix>, falling back to HOLT_<suffix>.
//
// The new spelling wins even when both are set, because during the cutover
// something sets both deliberately: this tool exports both spellings into every
// hook (config.hookEnv), so a hook that re-exports what it was handed hands
// back a pair. Preferring the new one means the updated half of the machine is
// the half that decides, which is the direction that ends the transition.
func Getenv(suffix string) string {
	v, _ := Lookup(suffix)
	return v
}

// Lookup is Getenv plus the spelling it actually found, for the one caller that
// has to name the variable back to the operator: a message about a bad value is
// useless if it names a variable the operator never set.
func Lookup(suffix string) (value, key string) {
	if v := os.Getenv(Prefix + suffix); v != "" {
		return v, Prefix + suffix
	}
	if v := os.Getenv(OldPrefix + suffix); v != "" {
		return v, OldPrefix + suffix
	}
	return "", Prefix + suffix
}

// Pair renders one variable under BOTH spellings, for a child's environment.
//
// This is the export half, and it is the half that actually keeps an old
// consumer alive: haus's lane hooks read HOLT_NAME, HOLT_REPO, HOLT_PATH,
// HOLT_MAIN, HOLT_CHAT and HOLT_COMMAND by those names, and a new binary that
// emitted only the new spelling would blank the bar on a machine whose haus
// hadn't been flipped yet. New name first, for a human reading `env`.
func Pair(suffix, value string) []string {
	return []string{Prefix + suffix + "=" + value, OldPrefix + suffix + "=" + value}
}

// Dir chooses between a scruff-named directory and its holt-named predecessor.
//
// The new name wins when it exists, AND when neither does — a machine that has
// never run this tool should create its config under the name it is going to
// keep. The old path is returned only when it is the one actually holding this
// machine's files, which is every machine that ran holt before this release.
//
// Deliberately a stat and not a migration: moving an operator's config is not
// this release's job (docs/rename.md decision 2 defers the one move there is),
// and a fallback that reads is reversible in a way a fallback that writes is
// not. ~/.config/holt is also routinely a read-only symlink into a Nix store on
// the machines that matter most here, so "just move it" isn't available anyway.
func Dir(newPath, oldPath string) string {
	if newPath == "" {
		return oldPath
	}
	if oldPath == "" {
		return newPath
	}
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if _, err := os.Stat(oldPath); err == nil {
		return oldPath
	}
	return newPath
}

// InvokedByOldName reports whether this process was started as `holt` rather
// than `scruff` — the binary ships under both, as one file and a symlink, so
// argv[0] is the only thing that can tell them apart.
func InvokedByOldName(argv0 string) bool {
	return filepath.Base(argv0) == OldName
}
