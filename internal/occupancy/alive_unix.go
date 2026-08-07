//go:build unix

package occupancy

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid names a live process.
//
// Signal 0 is the portable existence check: the kernel runs the permission
// check and locates the target, then delivers nothing. ESRCH is the ONLY answer
// that means gone — EPERM means the process is alive and owned by somebody
// else, which for occupancy purposes is just as occupied, and anything else is
// a question we failed to ask. Both resolve to "alive", because the failure
// direction here must be a lease that lingers, never a checkout reaped out from
// under a running session.
func processAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
