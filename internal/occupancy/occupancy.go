// Package occupancy answers the one question the reap is forbidden to guess at:
// is somebody standing in this checkout?
//
// # Why this is a provider set and not a function
//
// Until now the answer was one `lsof -d cwd` dump. That is macOS/BSD-shaped, it
// is absent from most containers, and it presumes the thing standing in a
// lane is a process with its cwd in its checkout — true of a zellij pane and
// false of nearly everything else. An embedder driving holt as a substrate
// (SPEC.md §14) has no panes at all: its sessions are connections, and only it
// can say which of them are live.
//
// So occupancy is a set of providers folded into one Report, and the fold has
// exactly one rule that matters:
//
//	A provider may always assert PRESENCE.
//	Only a provider that can enumerate every possible occupant may assert ABSENCE.
//
// lsof enumerates every process on the machine, so a dump that does not mention
// a path is real evidence nobody is there. A lease file is the opposite: it is
// written only by clients that opted in, so "no lease" means "nobody told me",
// never "nobody is here". Letting leases vouch for absence by default would reap
// the pane of anyone who simply cd'd in — invariant 2, broken by the tool whose
// whole purpose is invariant 2.
//
// Hence Report.Known reports true only when SOME provider vouched for absence,
// and unknown still resolves to keep, exactly as it did when lsof was the only
// answer.
package occupancy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TTL is how long a lease with no watchable process outlives its last
// heartbeat. It bounds only the pid-less case; a lease naming a live pid needs
// no refresh at all. See leaseLive.
const TTL = 90 * time.Second

// Provider is one way of answering "who is standing in a checkout?".
type Provider interface {
	// Name identifies the provider in degraded-mode messages.
	Name() string

	// Scan reports every path this provider observes as held, and whether a
	// path's ABSENCE from that list is evidence of an empty checkout or merely
	// an absence of evidence. See the package comment — this second return is
	// the whole safety model.
	Scan() (held []string, vouchesForAbsence bool)
}

// Report is the folded answer for one sweep. Providers are scanned once per
// sweep, never once per lane: the lsof dump alone is ~0.2s.
type Report struct {
	held    []string
	vouched []string
}

// Known reports whether any provider could vouch for absence. False means the
// question was unanswerable, and every caller must resolve that to *keep*.
func (r Report) Known() bool { return len(r.vouched) > 0 }

// Occupied reports whether path — or anything beneath it — is held. The prefix
// match is the point: a pane's cwd is usually a subdirectory of the checkout,
// not the checkout root.
func (r Report) Occupied(path string) bool {
	for _, h := range r.held {
		if h == path || strings.HasPrefix(h, path+"/") {
			return true
		}
	}
	return false
}

// Vouching names the providers that answered for absence, for diagnostics.
func (r Report) Vouching() []string { return r.vouched }

// Collect folds every provider into one Report.
func Collect(providers ...Provider) Report {
	var r Report
	for _, p := range providers {
		held, vouches := p.Scan()
		r.held = append(r.held, held...)
		if vouches {
			r.vouched = append(r.vouched, p.Name())
		}
	}
	return r
}

// ── lsof ─────────────────────────────────────────────────────────────────────

type lsofProvider struct{}

// LSOF is the machine-wide cwd scanner: one dump of every process's working
// directory. It enumerates every occupant there can be, so it vouches for
// absence — the only built-in provider that does.
func LSOF() Provider { return lsofProvider{} }

func (lsofProvider) Name() string { return "lsof" }

func (lsofProvider) Scan() ([]string, bool) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, false
	}
	out, err := exec.Command("lsof", "-w", "-d", "cwd", "-F", "n").Output()
	if err != nil && len(out) == 0 {
		return nil, false
	}
	var cwds []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			cwds = append(cwds, strings.TrimSpace(line[1:]))
		}
	}
	// An empty dump is a broken lsof, not an empty machine — there is always at
	// least one process with a cwd. Reading it as "nobody, anywhere" would reap
	// every landed checkout on the machine in one run.
	if len(cwds) == 0 {
		return nil, false
	}
	return cwds, true
}

// ── leases ───────────────────────────────────────────────────────────────────

type leaseProvider struct {
	dir  string
	sole bool
}

// Leases reads the heartbeat directory: one file per checkout an opted-in
// client claims to be working in.
//
// sole declares that holt-spawned clients are the ONLY way a checkout gets used
// in this deployment — true for an orchestrator that owns every session it
// serves, and false on a developer machine, where a human can always just cd in
// behind holt's back. It is the one switch that lets leases answer for absence,
// and it must stay opt-in: defaulting it to true would silently convert "nobody
// heartbeated" into "nobody is there".
func Leases(dir string, sole bool) Provider { return leaseProvider{dir: dir, sole: sole} }

func (l leaseProvider) Name() string { return "leases" }

func (l leaseProvider) Scan() ([]string, bool) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		// No directory yet is not the same as an empty one: nothing has ever
		// leased here, so this provider has nothing to say in either direction.
		return nil, false
	}
	var held []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		file := filepath.Join(l.dir, e.Name())
		pid, path, ok := readLease(file)
		if !ok {
			continue
		}
		if !leaseLive(file, pid) {
			// Reclaim eagerly. A dead lease left on disk reads as occupancy to
			// anything that parses this directory less carefully than we do,
			// and it costs one unlink to be sure it never does.
			_ = os.Remove(file)
			continue
		}
		held = append(held, path)
	}
	return held, l.sole
}

// LeaseFile is the on-disk lease for a checkout: sha256 of the checkout path,
// first 12 hex. Same naming as the registry v1 rows in SPEC.md §2.1, so the two
// directories line up by eye when v1 lands.
func LeaseFile(dir, path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(dir, hex.EncodeToString(sum[:])[:12])
}

// Acquire writes or refreshes the lease on path, held on behalf of pid.
//
// pid is the process whose death releases the lease. A caller that exec's holt
// wants its OWN pid here, not holt's — holt exits immediately, and a lease
// watching an exited process is a lease that was never taken. Pass 0 when there
// is no local process to watch (a client on the far side of a container or a
// socket); the lease then lives on its heartbeat alone and expires after TTL.
func Acquire(dir, path string, pid int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Write then rename: a scan that lands mid-write must never see a half
	// lease and drop it as corrupt. Rename also refreshes mtime, which is what
	// makes this function double as the heartbeat.
	tmp, err := os.CreateTemp(dir, ".lease-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strconv.Itoa(pid) + "\t" + path + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes 0600 files. A lease is meant to be READ by any holt on
	// the machine — the sweep that honours it may well be a different
	// invocation than the client that took it — so widen it deliberately rather
	// than leaving a lease that reads as garbage to everyone but its author.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), LeaseFile(dir, path))
}

// Release drops the lease on path. A lease that is already gone is a success:
// release must be safe to call from a cleanup path that cannot know whether it
// ran before.
func Release(dir, path string) error {
	err := os.Remove(LeaseFile(dir, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readLease(file string) (pid int, path string, ok bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, "", false
	}
	pidField, path, found := strings.Cut(strings.TrimRight(string(b), "\n"), "\t")
	if !found || path == "" {
		return 0, "", false
	}
	pid, err = strconv.Atoi(pidField)
	if err != nil {
		return 0, "", false
	}
	return pid, path, true
}

// leaseLive decides whether a lease still means anything.
//
// A named pid settles it outright, in both directions: the kernel is a better
// witness than any timestamp, it answers the instant a client is killed rather
// than TTL later, and a client that holds a lease for an eight-hour session
// never has to prove it is still there. The TTL exists only for the pid-less
// case, where freshness is the only evidence on offer.
func leaseLive(file string, pid int) bool {
	if pid > 0 {
		return processAlive(pid)
	}
	fi, err := os.Stat(file)
	return err == nil && time.Since(fi.ModTime()) < TTL
}
