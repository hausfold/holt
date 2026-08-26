package commands

import "testing"

// The porcelain reaching dirtyNote has been through gitx.Run's TrimSpace, so
// the first line of an unstaged-only change arrives one column short. Both
// shapes have to parse, and a rename's tail has to survive whole.
func TestPorcelainPath(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{" M modules/core/haus.sh", "modules/core/haus.sh"}, // as git prints it
		{"M modules/core/haus.sh", "modules/core/haus.sh"},  // after TrimSpace ate column 0
		{" D gone.txt", "gone.txt"},                         //
		{"D gone.txt", "gone.txt"},                          //
		{"?? untracked.txt", "untracked.txt"},               // both columns filled
		{"MM staged-and-not.txt", "staged-and-not.txt"},     //
		{"M  staged.txt", "staged.txt"},                     //
		{"R  old.txt -> new.txt", "old.txt -> new.txt"},     // the tail is kept
		{" M a file with spaces.txt", "a file with spaces.txt"},
		{`?? "caf\303\251.txt"`, "café.txt"}, // C-quoting is undone
	} {
		if got := porcelainPath(c.line); got != c.want {
			t.Errorf("porcelainPath(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
