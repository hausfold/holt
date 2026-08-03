package commands

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// rescuePATH makes holt survive being invoked with no PATH at all.
//
// This is a cutover requirement, not a nicety: Claude Code fires the
// WorktreeCreate/WorktreeRemove hooks with a bare environment, and holt shells
// out to `git` for everything and `gh` for PR state. Without this, the hook that
// is the tool's whole reason for existing fails at the first `git` call — and it
// fails at pane-open time, which is the worst possible moment to discover it.
// The bash `wt` carries the same rescue for the same reason.
//
// APPENDED, never prepended, and that ordering is load-bearing twice over:
//
//   - it is a rescue for the bare-PATH case, not an override of the caller's
//     environment — a user with their own git ahead of ours keeps it;
//   - the acceptance suite drives holt with shim `gh`/`lsof` early on PATH, and
//     prepending would let the real ones win every time, making the whole suite
//     silently test the machine instead of the code.
//
// HOLT_PATH_RESCUE=0 turns the rescue off. It exists because the rescue and any
// test that asserts a client is NOT installed are in direct tension: the rescue
// re-adds the very profile bindir the client lives in, so narrowing PATH cannot
// hide it. The bash `wt` has no such switch, which is why the equivalent test
// there can only be skipped. It is also the honest knob for anyone who wants
// holt to resolve strictly against the PATH they handed it.
func rescuePATH() {
	if os.Getenv("HOLT_PATH_RESCUE") == "0" {
		return
	}
	current := os.Getenv("PATH")

	candidates := []string{
		"/run/current-system/sw/bin",          // nix-darwin / NixOS system profile
		"/nix/var/nix/profiles/default/bin",   // single-user nix
		"/opt/homebrew/bin", "/usr/local/bin", // homebrew, both architectures
		"/usr/bin", "/bin", // the floor
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		candidates = append([]string{
			filepath.Join("/etc/profiles/per-user", u.Username, "bin"),
			filepath.Join(u.HomeDir, ".nix-profile", "bin"),
		}, candidates...)
	}

	have := map[string]bool{}
	for _, p := range filepath.SplitList(current) {
		have[p] = true
	}
	var add []string
	for _, c := range candidates {
		if !have[c] {
			add = append(add, c)
		}
	}
	if len(add) == 0 {
		return
	}
	// Guard the separator: an empty inherited PATH would otherwise produce a
	// leading ':', which means "the current directory" — and that would let any
	// repo drop a fake `git` in our lap.
	next := strings.Join(add, string(os.PathListSeparator))
	if current != "" {
		next = current + string(os.PathListSeparator) + next
	}
	_ = os.Setenv("PATH", next)
}
