package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func load(t *testing.T, body string) (*Config, []string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "holt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "holt", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load()
}

func TestLoadNoFileIsEveryDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, warnings := Load()
	if cfg.Agent != "" || len(cfg.Hooks) != 0 || len(warnings) != 0 {
		t.Fatalf("a missing config must be silent and empty, got %+v / %v", cfg, warnings)
	}
	if cfg.Defined(HookLanded) {
		t.Fatal("no config must mean no hooks")
	}
}

func TestLoadParsesKeysAndHooks(t *testing.T) {
	cfg, warnings := load(t, `
# the client a new worktree opens in
agent = "codex"   # trailing comment

[hooks]
resume   = "/usr/local/bin/holt-resume"
landed   = ["/usr/local/bin/holt-landed", "--strict", "a,b"]
preserve = '/usr/local/bin/holt-preserve'
`)
	if len(warnings) != 0 {
		t.Fatalf("clean config produced warnings: %v", warnings)
	}
	if cfg.Agent != "codex" {
		t.Fatalf("Agent = %q, want codex (the trailing comment must not survive)", cfg.Agent)
	}
	if got := cfg.Hooks["resume"]; !reflect.DeepEqual(got, []string{"/usr/local/bin/holt-resume"}) {
		t.Fatalf("a bare string hook must become a one-element argv, got %q", got)
	}
	want := []string{"/usr/local/bin/holt-landed", "--strict", "a,b"}
	if got := cfg.Hooks["landed"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("Hooks[landed] = %q, want %q (a comma inside quotes is data)", got, want)
	}
	if !cfg.Defined(HookPreserve) {
		t.Fatal("single-quoted hook value was dropped")
	}
}

// A typo in the config must cost you the line, not the pane. holt runs on the
// path of every worktree open, so a hard parse failure is a machine that won't
// spawn.
func TestLoadBadLineWarnsAndContinues(t *testing.T) {
	cfg, warnings := load(t, "agent = codex\n\n[hooks]\nlanded = \"/bin/true\"\n")
	if len(warnings) != 1 {
		t.Fatalf("want exactly one warning for the unquoted value, got %v", warnings)
	}
	if cfg.Agent != "" {
		t.Fatalf("an unparseable value must leave the key unset, got %q", cfg.Agent)
	}
	if !cfg.Defined(HookLanded) {
		t.Fatal("a bad line must not stop the rest of the file being read")
	}
}

// ── the hook protocol ────────────────────────────────────────────────────────

func hook(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Config{Hooks: map[string][]string{"landed": {path}}}
}

func TestAskExitCodes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    Answer
		refused bool
		warns   bool
	}{
		{"0 is yes", "#!/bin/sh\nexit 0\n", Yes, false, false},
		{"1 is no", "#!/bin/sh\nexit 1\n", No, false, false},
		{"2 is a safety refusal", "#!/bin/sh\nexit 2\n", No, true, false},
		{"3 is no opinion", "#!/bin/sh\nexit 3\n", Defer, false, false},
		// The accidental deaths. A hook killed by a typo'd command (127) or a
		// lost +x bit must never read as an opinion, and must never be silent.
		{"127 defers loudly", "#!/bin/sh\nexit 127\n", Defer, false, true},
		{"a crash defers loudly", "#!/bin/sh\nkill -SEGV $$\n", Defer, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := hook(t, tc.body).Ask("landed", map[string]string{"branch": "worktree-x"})
			if res.Answer != tc.want || res.Refused != tc.refused {
				t.Fatalf("Answer = %v refused=%v, want %v/%v", res.Answer, res.Refused, tc.want, tc.refused)
			}
			if (res.Warning != "") != tc.warns {
				t.Fatalf("Warning = %q, want warning: %v", res.Warning, tc.warns)
			}
		})
	}
}

func TestAskNoHookDefers(t *testing.T) {
	cfg := &Config{}
	if res := cfg.Ask("landed", nil); res.Answer != Defer || res.Warning != "" {
		t.Fatalf("an unconfigured hook must defer silently, got %+v", res)
	}
}

// Both channels carry the same situation, because a hook may be a program with
// a JSON parser or three lines of shell, and neither should have to become the
// other.
func TestAskDeliversPayloadOnStdinAndInEnv(t *testing.T) {
	cfg := hook(t, `#!/bin/sh
read -r body
[ "$HOLT_HOOK" = "landed" ] || { echo "HOLT_HOOK=$HOLT_HOOK" >&2; exit 1; }
[ "$HOLT_BRANCH" = "worktree-x" ] || { echo "HOLT_BRANCH=$HOLT_BRANCH" >&2; exit 1; }
[ "$HOLT_BASE_BRANCH" = "main" ] || { echo "HOLT_BASE_BRANCH=$HOLT_BASE_BRANCH" >&2; exit 1; }
case "$body" in *'"branch":"worktree-x"'*) ;; *) echo "stdin=$body" >&2; exit 1 ;; esac
echo '{"via": "release-train", "confidence": "certain"}'
exit 0
`)
	res := cfg.Ask("landed", map[string]string{"branch": "worktree-x", "base": "main"})
	if res.Answer != Yes {
		t.Fatalf("Answer = %v, want Yes (the hook asserts its own inputs and exits 1 on mismatch)", res.Answer)
	}
	if res.Data["via"] != "release-train" {
		t.Fatalf("stdout JSON not carried through: %+v", res.Data)
	}
}

// A predicate that prints prose has still answered with its exit code. Parsing
// is an enrichment, never a requirement.
func TestAskIgnoresNonJSONStdout(t *testing.T) {
	res := hook(t, "#!/bin/sh\necho looks landed to me\nexit 0\n").Ask("landed", nil)
	if res.Answer != Yes || res.Data != nil {
		t.Fatalf("got %+v, want Yes with no data", res)
	}
}

// A hook's env must never hand a lane's lifecycle state to holt as its state
// DIRECTORY. It did once: `open` exported HOLT_STATE=live, the pane it spawned
// inherited it, and every holt run in that pane wrote its machine-global state
// to the relative path "live" under the cwd — routinely a git checkout.
func TestHookEnvRenamesStateAwayFromHoltsOwnStateDir(t *testing.T) {
	env := hookEnv("open", map[string]string{
		"state": "live",
		"agent": "claude",
		"base":  "main",
		"path":  "/tmp/lane",
	})
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if _, ok := got["HOLT_STATE"]; ok {
		t.Errorf("HOLT_STATE is holt's state DIRECTORY — a hook must not set it; got %q", got["HOLT_STATE"])
	}
	if got["HOLT_LANE_STATE"] != "live" {
		t.Errorf("HOLT_LANE_STATE = %q, want %q", got["HOLT_LANE_STATE"], "live")
	}
	if _, ok := got["HOLT_AGENT"]; ok {
		t.Errorf("HOLT_AGENT is holt's one-invocation client override — a hook must not set it; got %q", got["HOLT_AGENT"])
	}
	if got["HOLT_LANE_AGENT"] != "claude" {
		t.Errorf("HOLT_LANE_AGENT = %q, want %q", got["HOLT_LANE_AGENT"], "claude")
	}
	if got["HOLT_BASE_BRANCH"] != "main" {
		t.Errorf("HOLT_BASE_BRANCH = %q, want %q", got["HOLT_BASE_BRANCH"], "main")
	}
	if got["HOLT_PATH"] != "/tmp/lane" {
		t.Errorf("HOLT_PATH = %q, want %q", got["HOLT_PATH"], "/tmp/lane")
	}
}
