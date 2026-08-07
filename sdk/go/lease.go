package holt

import (
	"context"
	"sync"
	"time"
)

// defaultRefreshInterval is comfortably under the 90s TTL
// (internal/occupancy.TTL) that applies when there's no pid to watch.
const defaultRefreshInterval = 60 * time.Second

// LeaseOptions configures Lease.
type LeaseOptions struct {
	// PID, when non-zero, ties the lease to a real local process: the
	// kernel drops it the instant that pid dies, so no refresh loop runs
	// at all and RefreshInterval is ignored.
	PID int
	// RefreshInterval overrides the refresh cadence used when PID is 0.
	// Zero means defaultRefreshInterval (60s).
	RefreshInterval time.Duration
}

// Lease holds an occupancy lease for as long as it's open, refreshing it
// on an interval comfortably under the 90s TTL. This is the primitive an
// embedder's "session" (a connection, not a cwd — SPEC.md §14.2) should
// hold from connect to disconnect:
//
//	lease := c.Lease(ctx, laneDir, holt.LeaseOptions{})
//	// ... serve the session ...
//	lease.Release(context.Background())
//
// A lease can only SAVE a lane from Reap, never condemn one — "nobody
// leased it" isn't proof nobody's there (SPEC.md §14.2).
type Lease struct {
	client *Client
	path   string

	mu       sync.Mutex
	released bool
	cancel   context.CancelFunc
	done     chan struct{} // closed once the refresh loop has exited, so Release can wait on it
}

// Lease takes an occupancy lease and returns a handle that refreshes it
// in the background until Release is called or ctx is canceled. The
// first Heartbeat is fired in the background, not awaited here — a
// constructor can't return an error without an extra bool/tuple wart, so
// a failure to take the lease surfaces on the next refresh the way a
// missed refresh always does (best-effort, self-healing on the next
// tick). Call List or Heartbeat directly first if you need the initial
// take to be synchronous.
//
// Pass a non-zero PID in opts instead when the lease should track a real
// local process — the OS then drops it the instant that pid dies, no
// refresh loop needed.
func (c *Client) Lease(ctx context.Context, path string, opts LeaseOptions) *Lease {
	loopCtx, cancel := context.WithCancel(ctx)
	l := &Lease{client: c, path: path, cancel: cancel, done: make(chan struct{})}

	if opts.PID != 0 {
		go func() {
			defer close(l.done)
			_ = c.Heartbeat(loopCtx, path, opts.PID)
		}()
		return l
	}

	interval := opts.RefreshInterval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	go func() {
		defer close(l.done)
		_ = c.Heartbeat(loopCtx, path, 0) // take it now
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				_ = c.Heartbeat(loopCtx, path, 0) // best-effort; a miss self-heals on the next tick
			}
		}
	}()
	return l
}

// Release drops the lease and stops refreshing it. Safe to call more
// than once; ctx governs only the final ReleaseHeartbeat call.
func (l *Lease) Release(ctx context.Context) error {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	l.released = true
	l.mu.Unlock()

	l.cancel()
	<-l.done
	return l.client.ReleaseHeartbeat(ctx, l.path)
}
