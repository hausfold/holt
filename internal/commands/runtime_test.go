package commands

import (
	"strings"
	"testing"

	"github.com/hausfold/holt/internal/exitcode"
)

// These cover RuntimeCmd's argv parsing and the pre-lookup checks in
// resolveRuntime — every case here returns before touching the registry, so a
// bare zero-value Env is enough. Lane resolution itself (matchLane, a real
// registry) is exercised end-to-end in the plan's manual verification, not
// here — this package has no existing fixture for a populated registry.
func TestRuntimeCmdUsageErrors(t *testing.T) {
	e := &Env{}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "holt runtime <up|enter|down>"},
		{"unknown verb", []string{"sideways", "lane", "--backend", "tart"}, "holt runtime <up|enter|down>"},
		{"missing lane name", []string{"up", "--backend", "tart"}, "name a lane"},
		{"unknown flag", []string{"up", "lane", "--nope"}, "unknown flag"},
		{"--backend with no value", []string{"up", "lane", "--backend"}, "needs an id"},
		{"two lane names", []string{"up", "one", "two", "--backend", "tart"}, "at most one lane name"},
		{"missing backend", []string{"up", "lane"}, "name a backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := e.RuntimeCmd(tc.args)
			if err == nil {
				t.Fatal("want a usage error, got nil")
			}
			if got := exitcode.Of(err); got != exitcode.Usage {
				t.Errorf("exit code = %d, want Usage (1); err = %v", got, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}
