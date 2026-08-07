package occupancy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fake is a provider with no side effects, so the fold can be tested without a
// machine attached.
type fake struct {
	name    string
	held    []string
	vouches bool
}

func (f fake) Name() string                           { return f.name }
func (f fake) Scan() ([]string, bool)                 { return f.held, f.vouches }
func provider(n string, v bool, h ...string) Provider { return fake{name: n, held: h, vouches: v} }

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
	if held, _ := Leases(dir, false).Scan(); len(held) != 1 || held[0] != "/w/held" {
		t.Fatalf("Scan() = %v, want [/w/held]", held)
	}
	if err := Release(dir, "/w/held"); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 0 {
		t.Fatalf("after Release, Scan() = %v, want empty", held)
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
		t.Errorf("held = %v, want none", held)
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
		t.Fatalf("held = %v, want none — the holder is gone", held)
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
		t.Fatalf("held = %v, want the lease kept — its holder is alive", held)
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
		t.Fatalf("a fresh pid-less lease must hold, got %v", held)
	}
	stale := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(LeaseFile(dir, "/w/remote"), stale, stale); err != nil {
		t.Fatal(err)
	}
	if held, _ := Leases(dir, false).Scan(); len(held) != 0 {
		t.Fatalf("a stale pid-less lease must expire, got %v", held)
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
	if len(held) != 1 || held[0] != "/w/good" {
		t.Fatalf("held = %v, want just the parseable lease", held)
	}
}
