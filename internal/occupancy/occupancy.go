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
//
// # Why a Holder and not a path
//
// Presence is kept WITH ITS WITNESS, and that is the difference between a
// refusal a human can act on and one they can only believe. lsof does not
// observe "a pane"; it observes "pid 46864, node, cwd here", and those are not
// the same claim — a Next.js telemetry daemon orphaned to pid 1 five days ago
// pins a lane exactly as hard as a live agent, and reads identically once the
// pid is thrown away. A user told "a pane is open in it" goes looking for a
// window that does not exist, finds nothing, and concludes holt is lying;
// told "pid 46864 node", they kill it in one line. Same verdict, same
// invariant — the only thing that changed is that the evidence survives.
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

// Holder is one observed occupant: what is standing there, and where.
//
// Every field but Path is best-effort — a provider that can only name a path
// says so by leaving PID zero, and the fold is identical either way. Nothing in
// the safety model reads these; they exist to be PRINTED.
type Holder struct {
	Path string // the cwd (or leased path) actually observed
	PID  int    // 0 when the provider cannot name a process
	Cmd  string // the process's short name, "" when unknown
	Via  string // the provider that saw it — filled in by Collect
}

// String renders one holder as the shortest thing a human can act on.
func (h Holder) String() string {
	switch {
	case h.PID > 0 && h.Cmd != "":
		return "pid " + strconv.Itoa(h.PID) + " " + h.Cmd
	case h.PID > 0:
		return "pid " + strconv.Itoa(h.PID) + " (" + h.Via + ")"
	default:
		return h.Via
	}
}

// Provider is one way of answering "who is standing in a checkout?".
type Provider interface {
	// Name identifies the provider in degraded-mode messages.
	Name() string

	// Scan reports every occupant this provider observes, and whether a path's
	// ABSENCE from that list is evidence of an empty checkout or merely an
	// absence of evidence. See the package comment — this second return is the
	// whole safety model.
	Scan() (held []Holder, vouchesForAbsence bool)
}

// Report is the folded answer for one sweep. Providers are scanned once per
// sweep, never once per lane: the lsof dump alone is ~0.2s.
type Report struct {
	held    []Holder
	vouched []string
}

// Known reports whether any provider could vouch for absence. False means the
// question was unanswerable, and every caller must resolve that to *keep*.
func (r Report) Known() bool { return len(r.vouched) > 0 }

// Occupied reports whether path — or anything beneath it — is held. The prefix
// match is the point: a pane's cwd is usually a subdirectory of the checkout,
// not the checkout root.
func (r Report) Occupied(path string) bool { return len(r.Holders(path)) > 0 }

// Holders names every occupant of path, for the refusal that has to explain
// itself. Same match as Occupied, which is not a coincidence: the two must
// never be able to disagree, so Occupied is defined in terms of this.
func (r Report) Holders(path string) []Holder {
	var out []Holder
	for _, h := range r.held {
		if h.Path == path || strings.HasPrefix(h.Path, path+"/") {
			out = append(out, h)
		}
	}
	return out
}

// Vouching names the providers that answered for absence, for diagnostics.
func (r Report) Vouching() []string { return r.vouched }

// Collect folds every provider into one Report.
func Collect(providers ...Provider) Report {
	var r Report
	for _, p := range providers {
		held, vouches := p.Scan()
		for _, h := range held {
			h.Via = p.Name()
			r.held = append(r.held, h)
		}
		if vouches {
			r.vouched = append(r.vouched, p.Name())
		}
	}
	return r
}

// Describe renders holders as one clause of evidence, relative to the checkout
// they are holding — a cwd DEEPER than the root is worth saying out loud,
// because "in node_modules/next" is usually the whole diagnosis.
//
// Capped like the dirty listing, and for the same reason: a checkout with a
// build tree running in it should report a line, not a screenful.
func Describe(root string, holders []Holder) string {
	shown := holders
	if len(shown) > HoldersShown {
		shown = shown[:HoldersShown]
	}
	parts := make([]string, 0, len(shown))
	for _, h := range shown {
		s := h.String()
		if rel := relBelow(root, h.Path); rel != "" {
			s += " in " + rel
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, ", ")
	if n := len(holders) - len(shown); n > 0 {
		out += ", +" + strconv.Itoa(n) + " more"
	}
	return out
}

// HoldersShown caps the named occupants in a single refusal.
const HoldersShown = 3

func relBelow(root, path string) string {
	if root == "" || path == root || !strings.HasPrefix(path, root+"/") {
		return ""
	}
	return path[len(root)+1:]
}

// ── lsof ─────────────────────────────────────────────────────────────────────

type lsofProvider struct{}

// LSOF is the machine-wide cwd scanner: one dump of every process's working
// directory. It enumerates every occupant there can be, so it vouches for
// absence — the only built-in provider that does.
func LSOF() Provider { return lsofProvider{} }

func (lsofProvider) Name() string { return "lsof" }

func (lsofProvider) Scan() ([]Holder, bool) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, false
	}
	// -F pcn, not -F n: the pid and command name cost nothing to ask for and
	// are the entire difference between a refusal that can be checked and one
	// that can only be taken on faith. Field sets arrive as a stream — `p`
	// opens a process, `c` names it, and every `n` after that belongs to the
	// process last opened — so the parse is a running pair, not a per-line one.
	out, err := exec.Command("lsof", "-w", "-d", "cwd", "-F", "pcn").Output()
	if err != nil && len(out) == 0 {
		return nil, false
	}
	var held []Holder
	pid, cmd := 0, ""
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			// A new process set. The command name belongs to the process that
			// opened it, so it is cleared here rather than carried over — an
			// lsof that omits `c` for one process must not label it with the
			// previous one's name.
			pid, _ = strconv.Atoi(strings.TrimSpace(line[1:]))
			cmd = ""
		case 'c':
			cmd = strings.TrimSpace(line[1:])
		case 'n':
			held = append(held, Holder{
				Path: strings.TrimSpace(line[1:]),
				PID:  pid,
				Cmd:  cmd,
			})
		}
	}
	// An empty dump is a broken lsof, not an empty machine — there is always at
	// least one process with a cwd. Reading it as "nobody, anywhere" would reap
	// every landed checkout on the machine in one run.
	if len(held) == 0 {
		return nil, false
	}
	return held, true
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

func (l leaseProvider) Scan() ([]Holder, bool) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		// No directory yet is not the same as an empty one: nothing has ever
		// leased here, so this provider has nothing to say in either direction.
		return nil, false
	}
	var held []Holder
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
		held = append(held, Holder{Path: path, PID: pid})
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
