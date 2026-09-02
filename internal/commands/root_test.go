package commands

import (
	"strings"
	"testing"
)

// `scruff reap --help` swept: help was spelled only at the top level and Reap
// never looked at its arguments. These cover the scan that fixes it — the bats
// suite proves the sweep no longer happens, this proves the scan tells a flag
// from the DATA that follows one, which is the half a black-box test can only
// reach by opening a lane.

func TestHelpAsked(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"the flag itself", []string{"--help"}, true},
		{"short", []string{"-h"}, true},
		{"after a lane name", []string{"sparkle", "--help"}, true},
		{"nothing to ask", []string{}, false},
		{"a value, not a question", []string{"--prompt", "--help"}, false},
		{"a file named like a flag", []string{"--prompt-file", "-h"}, false},
		{"everything past -- is the client's", []string{"--", "--help"}, false},
		{"a flag we do reject elsewhere", []string{"--dry-run"}, false},
	} {
		if got := helpAsked(tc.args); got != tc.want {
			t.Errorf("%s: helpAsked(%q) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestVerbUsage(t *testing.T) {
	reap := verbUsage("reap")
	if !strings.Contains(reap, "scruff reap") {
		t.Fatalf("reap's own line is missing:\n%s", reap)
	}
	// Every block header in there must be reap's own. Prose that MENTIONS
	// another verb (reap's undo is `scruff reaped`) is the point, not a leak —
	// what must not appear is a second verb's entry.
	for _, line := range strings.Split(reap, "\n") {
		if !strings.HasPrefix(line, "  scruff") {
			continue
		}
		if f := strings.Fields(line); len(f) < 2 || f[1] != "reap" {
			t.Errorf("another verb's entry came along: %q", line)
		}
	}

	// A block's continuation lines belong to it — they carry the flags.
	if !strings.Contains(verbUsage("new"), "--prompt-file") {
		t.Error("new's help dropped the lines under it")
	}
	// All four hook spellings are one verb.
	if h := verbUsage("hook"); !strings.Contains(h, "hook create") || !strings.Contains(h, "hook notify") {
		t.Errorf("hook's help is partial:\n%s", h)
	}
	// A verb the usage block never names is better served the whole manual than
	// an empty answer.
	if verbUsage("resume") != usage {
		t.Error("an undocumented verb should fall back to the full usage")
	}
}

func TestNoArgsAndOneArg(t *testing.T) {
	if err := noArgs("reap", nil); err != nil {
		t.Errorf("the bare verb must still run: %v", err)
	}
	if err := noArgs("reap", []string{"--dry-run"}); err == nil {
		t.Error("reap ran on an argument it cannot explain")
	}

	if got, err := oneArg("drop", []string{"sparkle"}); err != nil || got != "sparkle" {
		t.Errorf("oneArg = %q, %v", got, err)
	}
	if got, err := oneArg("resume", []string{"--pick", "sparkle"}, "--pick"); err != nil || got != "sparkle" {
		t.Errorf("an accepted flag must not eat the name: %q, %v", got, err)
	}
	if _, err := oneArg("resume", []string{"--picky", "sparkle"}, "--pick"); err == nil {
		t.Error("a misspelled flag resolved to a lane anyway")
	}
	if _, err := oneArg("park", []string{"two", "words"}); err == nil {
		t.Error("a second bare word was silently dropped")
	}
}
