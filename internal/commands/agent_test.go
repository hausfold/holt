package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustWorktree edits a file holt does not own — Claude Code's ~/.claude.json,
// ~180KB of that client's own state — so the thing under test is as much what it
// LEAVES ALONE as what it writes. The re-encode is the sharp edge: decoding JSON
// into map[string]any turns every number into a float64 unless you ask for
// json.Number, and marshalling that back writes `1.778838900185e+12` where a
// millisecond timestamp used to be. That corruption would be silent, in the
// user's client config, on a code path whose entire purpose is convenience.

const claudeConfig = `{
  "numStartups": 1202,
  "installMethod": "native",
  "oauthAccount": {"accountUuid": "abc-123"},
  "projects": {
    "/repo": {
      "hasTrustDialogAccepted": true,
      "lastSessionModified": 1778838900185,
      "lastFpsAverage": 6.07,
      "history": [{"display": "hello"}]
    },
    "/untrusted": {"hasTrustDialogAccepted": false}
  }
}`

func withHome(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readProjects(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("wrote invalid JSON: %v", err)
	}
	projects, _ := doc["projects"].(map[string]any)
	return projects
}

func trusted(projects map[string]any, dir string) bool {
	entry, _ := projects[dir].(map[string]any)
	ok, _ := entry["hasTrustDialogAccepted"].(bool)
	return ok
}

func TestTrustWorktreeInheritsFromTrustedParent(t *testing.T) {
	path := withHome(t, claudeConfig)
	trustWorktree("claude", "/repo", "/wt/feature")

	projects := readProjects(t, path)
	if !trusted(projects, "/wt/feature") {
		t.Error("worktree of a trusted repo should not face the trust dialog")
	}
	if !trusted(projects, "/repo") {
		t.Error("the parent's own trust must survive the rewrite")
	}
}

// The one thing this must never do: decide on the user's behalf.
func TestTrustWorktreeRefusesUntrustedParent(t *testing.T) {
	path := withHome(t, claudeConfig)
	trustWorktree("claude", "/untrusted", "/wt/feature")

	if projects := readProjects(t, path); trusted(projects, "/wt/feature") {
		t.Error("granted trust the user never gave the parent repo")
	}
}

func TestTrustWorktreeIgnoresOtherClients(t *testing.T) {
	path := withHome(t, claudeConfig)
	for _, agent := range []string{"codex", "opencode", ""} {
		trustWorktree(agent, "/repo", "/wt/"+agent)
	}
	projects := readProjects(t, path)
	for _, agent := range []string{"codex", "opencode", ""} {
		if _, ok := projects["/wt/"+agent]; ok {
			t.Errorf("%q is not Claude Code and has no trust model to seed", agent)
		}
	}
}

// The corruption test. A large integer must come back as the same integer, not
// as the float64 a naive map round-trip would produce.
func TestTrustWorktreePreservesEverythingElse(t *testing.T) {
	path := withHome(t, claudeConfig)
	trustWorktree("claude", "/repo", "/wt/feature")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`1778838900185`,       // an int must not become 1.778838900185e+12
		`6.07`,                // …and a float must stay a float
		`"numStartups": 1202`, // untouched top-level keys survive
		`"accountUuid": "abc-123"`,
		`"display": "hello"`, // nested arrays/objects survive
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rewrite lost or mangled %s\n--- got ---\n%s", want, text)
		}
	}
	if os.Getenv("CI") == "" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
			t.Errorf("a file holding credentials must stay 0600, got %o", fi.Mode().Perm())
		}
	}
}

// Every failure mode is a no-op, because the cost of getting this wrong (a
// clobbered client config) dwarfs the cost of not doing it (one trust prompt).
func TestTrustWorktreeSurvivesABadConfig(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		path := withHome(t, "")
		trustWorktree("claude", "/repo", "/wt/feature")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("must not conjure a config Claude Code never wrote")
		}
	})

	for name, body := range map[string]string{
		"unparseable":   `{"projects": {`,
		"no projects":   `{"numStartups": 3}`,
		"not an object": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := withHome(t, body)
			trustWorktree("claude", "/repo", "/wt/feature")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != body {
				t.Errorf("rewrote a config it could not understand:\n%s", raw)
			}
		})
	}
}

// Re-running a spawn (or spawning twice into the same path) must not churn the
// file — this is the guard on "don't rewrite 180KB for nothing".
func TestTrustWorktreeIsIdempotent(t *testing.T) {
	path := withHome(t, claudeConfig)
	trustWorktree("claude", "/repo", "/wt/feature")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trustWorktree("claude", "/repo", "/wt/feature")
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("a second call rewrote a file it had nothing to add to")
	}
}

// ── the prompt is data, not flags ────────────────────────────────────────────
//
// The regression this pins: a Spawn Agent prompt pasted as a markdown list
// starts with `- `, and a bare argv element starting with a dash is an OPTION to
// every one of these clients. Pounce's box produced exactly that and the pane
// died on `error: unknown option '- https://…'` before the agent ever ran.

func TestStartArgvEndsOptionParsingBeforeThePrompt(t *testing.T) {
	const dashed = "- update the README\n- and its footer"

	cases := []struct {
		agent string
		image string
		want  []string
	}{
		{"claude", "", []string{"claude", "--", dashed}},
		{"codex", "", []string{"codex", "--", dashed}},
		{"codex", "/tmp/shot.png", []string{"codex", "-i", "/tmp/shot.png", "--", dashed}},
		{"opencode", "", []string{"opencode", "--prompt=" + dashed}},
	}
	for _, tc := range cases {
		spec, ok := specFor(tc.agent)
		if !ok {
			t.Fatalf("no spec for %q", tc.agent)
		}
		got := spec.start(tc.image, dashed)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("%s start argv = %q, want %q", tc.agent, got, tc.want)
		}
	}
}

// Whatever the prompt, it must never arrive as an argv element a parser could
// still read as a flag — the property, not the four spellings above.
func TestStartNeverHandsAClientABareDashedPrompt(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "opencode"} {
		spec, _ := specFor(agent)
		argv := spec.start("", "-x")
		for i, arg := range argv {
			if arg != "-x" {
				continue
			}
			if i == 0 || !terminatesOptions(argv[i-1]) {
				t.Errorf("%s: prompt at argv[%d] of %q is still option-parsed", agent, i, argv)
			}
		}
	}
}

func terminatesOptions(prev string) bool { return prev == "--" }
