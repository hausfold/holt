package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rewritePrefix is the registry rewrite's whole mechanism, and its failure
// modes are exact-prefix ones: a base named like a sibling, or a path that
// merely CONTAINS the base name, must survive untouched.
func TestRewritePrefixMatchesExactly(t *testing.T) {
	cases := []struct{ old, new, in, want string }{
		{"/base", "/new", "/base/alpha/sparkle", "/new/alpha/sparkle"},
		{"/base", "/new", "/base", "/new"}, // the base itself, in a Parent field
		{"/base", "/new", "/basement/lane", "/basement/lane"},
		{"/base", "/new", "/other/base/lane", "/other/base/lane"},
		{"/base", "/new", "/elsewhere", "/elsewhere"}, // a Main checkout, untouched
	}
	for _, c := range cases {
		if got := rewritePrefix(c.old, c.new, c.in); got != c.want {
			t.Errorf("rewritePrefix(%q, %q, %q) = %q, want %q", c.old, c.new, c.in, got, c.want)
		}
	}
}

// The refusals fire BEFORE anything moves. Each asserts the legacy tree is
// still standing — step 6's "nothing moved" direction, reached by not moving.
func TestMigrateBaseRefusesBeforeTouchingAnything(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCRUFF_BASE", "")
	t.Setenv("CLAUDE_WT_BASE", "")
	legacy := filepath.Join(home, ".cache", "claude-worktrees")
	scruffBase := filepath.Join(home, ".cache", "scruff")

	plant := func() {
		t.Helper()
		if err := os.MkdirAll(legacy, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, "registry.tsv"), []byte("lane\t/repo\tworktree-lane\t"+legacy+"/lane\t\tclaude\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	untouched := func() {
		t.Helper()
		if _, err := os.Stat(filepath.Join(legacy, "registry.tsv")); err != nil {
			t.Fatalf("the legacy registry vanished during a refusal: %v", err)
		}
		if _, err := os.Stat(scruffBase); err == nil {
			t.Fatal("a scruff-named base appeared during a refusal")
		}
	}

	// The legacy base is planted up front: every refusal below must leave it
	// exactly as it was — step 6's "nothing moved" direction, reached by
	// not moving.
	plant()

	// A base-path override means the operator owns the layout.
	t.Setenv("SCRUFF_BASE", "/mine")
	if err := (&Env{Base: legacy}).migrateBase(); err == nil {
		t.Fatal("SCRUFF_BASE set: migrate must refuse")
	}
	t.Setenv("SCRUFF_BASE", "")
	untouched()

	// Already migrated: the scruff-named registry answers, and the move is a
	// silent no-op that leaves both trees as they were.
	if err := os.MkdirAll(scruffBase, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scruffBase, "registry.tsv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (&Env{Base: legacy}).migrateBase(); err != nil {
		t.Fatalf("already-migrated base: migrate must be a silent no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "registry.tsv")); err != nil {
		t.Fatalf("the no-op disturbed the legacy base: %v", err)
	}
}

// The usage/no-registry path names what it looked for, because "nothing to
// migrate" and "you have no base at all" are different situations.
func TestMigrateBaseWithoutAnyBaseIsUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCRUFF_BASE", "")
	t.Setenv("CLAUDE_WT_BASE", "")

	err := (&Env{Base: filepath.Join(home, ".cache", "scruff")}).migrateBase()
	if err == nil {
		t.Fatal("migrate with no base anywhere must fail, not no-op silently")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("the error must name the registry it looked for: %v", err)
	}
}
