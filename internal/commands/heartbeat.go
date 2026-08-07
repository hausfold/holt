package commands

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/gitx"
	"github.com/nebelhaus/holt/internal/occupancy"
	"github.com/nebelhaus/holt/internal/ui"
)

// Heartbeat takes, refreshes, or drops the occupancy lease on a checkout.
//
// This is the seam `lsof` cannot cover, and the reason it exists is SPEC.md
// §14: a program embedding holt has no panes and no shell cwd'd anywhere, so
// the only thing that knows its sessions are live is the program itself. It
// says so here, and `reap` believes it.
//
// The lease is positive-only by default — it can save a checkout from the
// sweep, never condemn one. See occupancy.Leases for why that asymmetry is the
// whole safety model, and HOLT_OCCUPANCY=lease for the deployment that is
// entitled to drop it.
//
//	holt heartbeat [path]              take or refresh, held by the CALLING process
//	holt heartbeat [path] --pid N      held by pid N instead (0 = TTL-only)
//	holt heartbeat [path] --release    drop it
func (e *Env) Heartbeat(args []string) error {
	var (
		target  string
		release bool
		pid     = os.Getppid()
	)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--release":
			release = true
		case "--pid":
			i++
			if i >= len(args) {
				return exitcode.Usagef("holt heartbeat --pid needs a number")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return exitcode.Usagef("holt heartbeat --pid %q is not a pid", args[i])
			}
			pid = n
		default:
			if a == "" {
				continue
			}
			if a[0] == '-' {
				return exitcode.Usagef("unknown flag %q — try `holt --help`", a)
			}
			if target != "" {
				return exitcode.Usagef("holt heartbeat takes at most one path")
			}
			target = a
		}
	}

	path, err := leaseTarget(e.Cwd, target)
	if err != nil {
		return err
	}

	if release {
		if err := occupancy.Release(e.LeaseDir, path); err != nil {
			return err
		}
		ui.Say("released %s", path)
		return nil
	}
	if err := occupancy.Acquire(e.LeaseDir, path, pid); err != nil {
		return err
	}
	if pid == 0 {
		ui.Say("holding %s — no pid to watch, so refresh within %s", path, occupancy.TTL)
	} else {
		ui.Say("holding %s while pid %d lives", path, pid)
	}
	return nil
}

// leaseTarget resolves what a lease is actually being taken on.
//
// The sweep compares against CHECKOUT roots, so a lease named by some
// subdirectory has to normalise to the same string or it protects nothing. The
// git toplevel is that normalisation; a path outside any repo is taken at face
// value, because a caller naming a directory holt has never heard of is more
// likely to know something we don't than to have made a mistake.
func leaseTarget(cwd, arg string) (string, error) {
	if arg == "" {
		arg = cwd
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", exitcode.Usagef("no such path: %s", abs)
	}
	if top, err := gitx.Toplevel(abs); err == nil && top != "" {
		return top, nil
	}
	// EvalSymlinks so a lease taken through /var matches a registry row written
	// as /private/var — the same resolution gitx.Toplevel gives us for free on
	// the branch above, and the reason the bats fixtures resolve TMP up front.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}
