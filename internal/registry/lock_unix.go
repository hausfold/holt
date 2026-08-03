//go:build unix

package registry

import (
	"os"
	"syscall"
	"time"

	"github.com/nebelhaus/holt/internal/exitcode"
)

// lockTimeout bounds how long we wait for another holt to finish a mutation.
// Registry writes are milliseconds; anything past this is a wedged process, and
// blocking a pane's teardown forever is worse than failing loudly.
const lockTimeout = 5 * time.Second

// lock takes an exclusive flock on path, returning the release function.
//
// flock, not a lockfile: the kernel releases it when the process dies, so a
// crashed holt cannot wedge every future invocation. The bash version used a
// mkdir lock and needed a "break it after 5 seconds" escape hatch precisely
// because a directory outlives the process that made it.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, exitcode.Lockedf("another holt is holding the registry (%s) — retry in a moment", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
