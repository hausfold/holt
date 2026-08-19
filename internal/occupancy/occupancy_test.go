package occupancy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake is a provider with no side effects, so the fold can be tested without a
// machine attached.
type fake struct {
	name    string
	held    []Holder
	vouches bool
}

func (f fake) Name() string           { return f.name }
func (f fake) Scan() ([]Holder, bool) { return f.held, f.vouches }

func provider(n string, v bool, paths ...string) Provider {
	held := make([]Holder, 0, len(paths))
	for i, p := range paths {
		held = append(held, Holder{Path: p, PID: 100 + i, Cmd: "zsh"})
	}
	return fake{name: n, held: held, vouches: v}
}

// paths flattens a Scan for the assertions that only care about where.
func paths(held []Holder) []string {
	out := make([]string, 0, len(held))
	for _, h := range held {
		out = append(out, h.Path)
	}
	return out
}

// The fold's whole safety model in one table: presence unions, absence needs a
// witness. A provider that cannot enumerate every occupant must never be able
// to turn "I didn't see it" into "it isn't there".
func TestCollectFold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		providers []Provider
		wantKnown bool
		occupied  string
		free      string
	}{
		{
			name:      "a vouching provider answers in both directions",
			providers: []Provider{provider("lsof", true, "/w/busy")},
			wantKnown: true,
			occupied:  "/w/busy",
			free:      "/w/idle",
		},
		{
			name:      "a positive-only provider asserts presence but not absence",
			providers: []Provider{provider("leases", false, "/w/busy")},
			wantKnown: false,
			occupied:  "/w/busy",
			free:      "/w/idle",
		},
		{
			name:      "no provider at all is unknown, and unknown is not empty",
			providers: nil,
			wantKnown: false,
			free:      "/w/idle",
		},
		{
			name: "presence unions across providers",
			providers: []Provider{
				provider("lsof", true, "/w/one"),
				provider("leases", false, "/w/two"),
			},
			wantKnown: true,
			occupied:  "/w/two",
			free:      "/w/three",
		},
		{
			name: "one voucher is enough even when another provider abstains",
			providers: []Provider{
				provider("leases", false),
				provider("lsof", true, "/w/one"),
			},
			wantKnown: true,
			occupied:  "/w/one",
			free:      "/w/two",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Collect(tc.providers...)
			if r.Known() != tc.wantKnown {
				t.Fatalf("Known() = %v, want %v", r.Known(), tc.wantKnown)
			}
			if tc.occupied != "" && !r.Occupied(tc.occupied) {
				t.Errorf("Occupied(%q) = false, want true", tc.occupied)
			}
			if tc.free != "" && r.Occupied(tc.free) {
				t.Errorf("Occupied(%q) = true, want false", tc.free)
			}
		})
	}
}

// A pane's cwd is usually somewhere inside the checkout, not its root, so the
// match has to descend — without letting a sibling whose name merely starts the
// same way count as being inside it.
func TestOccupiedMatchesSubdirectoriesOnly(t *testing.T) {
	r := Collect(provider("lsof", true, "/w/api/src/deep"))
	if !r.Occupied("/w/api") {
		t.Error("a cwd below the checkout must count as occupying it")
	}
	if r.Occupied("/w/ap") {
		t.Error("a prefix that is not a path boundary must not match")
	}
	if r.Occupied("/w/api-old") {
		t.Error("a sibling sharing a name prefix must not match")
	}
}

func TestLeaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Acquire(dir, "/w/held", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 1 || held[0].Path != "/w/held" {
		t.Fatalf("Scan() = %v, want [/w/held]", paths(held))
	}
	if err := Release(dir, "/w/held"); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 0 {
		t.Fatalf("after Release, Scan() = %v, want empty", paths(held))
	}
	// Releasing twice is a cleanup path running after itself, not an error.
	if err := Release(dir, "/w/held"); err != nil {
		t.Errorf("second Release: %v", err)
	}
}

// sole is the embedder's switch, and it is the ONLY thing that lets a lease
// answer for absence.
func TestLeasesVouchOnlyWhenSole(t *testing.T) {
	dir := t.TempDir()
	if err := Acquire(dir, "/w/held", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if _, vouches := Leases(dir, false).Scan(); vouches {
		t.Error("a lease directory must not vouch for absence by default")
	}
	if _, vouches := Leases(dir, true).Scan(); !vouches {
		t.Error("sole leases must vouch for absence")
	}
}

// A lease directory that has never been written is not an empty machine.
func TestMissingLeaseDirIsSilentNotEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	held, vouches := Leases(missing, true).Scan()
	if len(held) != 0 {
		t.Errorf("held = %v, want none", paths(held))
	}
	if vouches {
		t.Error("a lease dir that does not exist must not vouch for anything")
	}
}

// The kernel is the witness: a lease dies with its process, without waiting out
// the TTL, and the dead file is reclaimed on sight.
func TestLeaseDiesWithItsProcess(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid

	if err := Acquire(dir, "/w/ghost", dead); err != nil {
		t.Fatal(err)
	}
	held, _ := Leases(dir, false).Scan()
	if len(held) != 0 {
		t.Fatalf("held = %v, want none — the holder is gone", paths(held))
	}
	if _, err := os.Stat(LeaseFile(dir, "/w/ghost")); !os.IsNotExist(err) {
		t.Error("a dead lease must be reclaimed, not left to be re-read")
	}
}

// A live pid needs no refresh — that is the point of watching it. An
// eight-hour session must not have to prove every 90s that it still exists.
func TestLiveHolderOutlivesTheTTL(t *testing.T) {
	dir := t.TempDir()
	if err := Acquire(dir, "/w/patient", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-10 * TTL)
	if err := os.Chtimes(LeaseFile(dir, "/w/patient"), stale, stale); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 1 {
		t.Fatalf("held = %v, want the lease kept — its holder is alive", paths(held))
	}
}

// pid 0 is the remote holder: nothing local to watch, so freshness is the only
// evidence there is.
func TestPidlessLeaseExpiresOnTTL(t *testing.T) {
	dir := t.TempDir()
	if err := Acquire(dir, "/w/remote", 0); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 1 {
		t.Fatalf("a fresh pid-less lease must hold, got %v", paths(held))
	}
	stale := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(LeaseFile(dir, "/w/remote"), stale, stale); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 0 {
		t.Fatalf("a stale pid-less lease must expire, got %v", paths(held))
	}
}

// Acquire doubles as the heartbeat: taking the same lease again refreshes it
// rather than piling up a second file.
func TestAcquireRefreshesInPlace(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Acquire(dir, "/w/held", 0); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d files in the lease dir, want 1", len(entries))
	}
}

// A garbled lease is skipped, never fatal, and never read as occupancy for some
// other path — the same reasoning the registry parser uses for a corrupt row.
func TestGarbageLeaseIsSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "garbage"), []byte("not a lease"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Acquire(dir, "/w/good", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	held, _ := Leases(dir, false).Scan()
	if len(held) != 1 || held[0].Path != "/w/good" {
		t.Fatalf("held = %v, want just the parseable lease", paths(held))
	}
}

// The lsof parse is a STREAM, not a line map: `p` opens a process, `c` names
// it, and every `n` after belongs to whichever process was opened last. Getting
// that wrong is worse than having no pid at all — it would attribute a cwd to
// the wrong process, and the whole point of carrying the pid is that a human
// can go and look at it.
func TestLSOFParsesFieldSets(t *testing.T) {
	dir := t.TempDir()
	// printf, not cat: PATH is replaced wholesale below so the fake lsof is the
	// only thing findable, and a builtin is the only command still guaranteed.
	script := "#!/bin/sh\nprintf '%s\\n' " +
		"p1 cinit fcwd n/ " + // the machine's own noise: a non-empty dump
		"p4242 cnode fcwd n/w/api/node_modules/next " +
		"p77 fcwd n/w/api\n"
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	r := Collect(LSOF())
	if !r.Known() {
		t.Fatal("a non-empty dump must vouch for absence")
	}
	held := r.Holders("/w/api")
	if len(held) != 2 {
		t.Fatalf("Holders = %v, want the two processes standing in /w/api", held)
	}
	if held[0].PID != 4242 || held[0].Cmd != "node" {
		t.Errorf("holder[0] = %+v, want pid 4242 node", held[0])
	}
	// The third set names no command. Carrying "node" forward would invent a
	// process that does not exist.
	if held[1].PID != 77 || held[1].Cmd != "" {
		t.Errorf("holder[1] = %+v, want pid 77 with no command", held[1])
	}
	if r.Occupied("/w/other") {
		t.Error("a path nothing stands in must stay free")
	}
}

// A dump lsof could not produce is still "no answer", not "nobody is anywhere" —
// the same rule as before pids were carried, restated because the emptiness
// check now looks at holders rather than raw paths.
func TestLSOFEmptyDumpVouchesForNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if r := Collect(LSOF()); r.Known() {
		t.Error("a broken lsof must not vouch for absence")
	}
}

// Describe is the whole user-visible payoff: a pid to look up, a command to
// recognise, and — when the cwd is deeper than the checkout — the subdirectory,
// which is usually the diagnosis on its own.
func TestDescribeNamesEvidence(t *testing.T) {
	root := "/w/api"
	got := Describe(root, []Holder{
		{Path: root, PID: 12, Cmd: "zsh", Via: "lsof"},
		{Path: root + "/node_modules/next", PID: 34, Cmd: "node", Via: "lsof"},
	})
	want := "pid 12 zsh, pid 34 node in node_modules/next"
	if got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}

	// A lease knows the pid but never the command, so the provider stands in
	// for it rather than the line reading like a nameless process.
	if got := Describe(root, []Holder{{Path: root, PID: 9, Via: "leases"}}); got != "pid 9 (leases)" {
		t.Errorf("Describe(lease) = %q", got)
	}

	// Capped, like the dirty listing — a checkout with a build running in it
	// reports a line, not a screenful.
	var many []Holder
	for i := 0; i < HoldersShown+2; i++ {
		many = append(many, Holder{Path: root, PID: i + 1, Cmd: "node", Via: "lsof"})
	}
	if got := Describe(root, many); !strings.HasSuffix(got, ", +2 more") {
		t.Errorf("Describe(many) = %q, want a +2 more tail", got)
	}
}
