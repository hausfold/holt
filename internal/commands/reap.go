package commands

import (
	"github.com/nebelhaus/holt/internal/ui"
)

// Reap sweeps every LANDED worktree now — parked ones, plus clean, landed
// checkouts that NO pane is sitting in.
//
// This is the idempotent backstop for when a pane ends WITHOUT firing the remove
// hook (a manual close, a reboot, a crash), and for `holt child` checkouts,
// which the hook never reaps.
func (e *Env) Reap() error {
	// A sweep that DELETES branches must ask the forge fresh. The listing's
	// 2-minute memo is right for an annotation and wrong here: a PR merged 30
	// seconds ago should reap on this run, and a PR reopened 30 seconds ago must
	// not.
	cacheTTL = 0

	res := e.reapSweep(sweepAll)

	if res.Degraded {
		ui.Say("no lsof — can't tell which checkouts have a pane open, so only PARKED worktrees were swept.")
	}
	for _, name := range res.Reaped {
		ui.Say("reaped %s", name)
	}
	for _, name := range res.SkippedLive {
		ui.Say("kept %s — a pane is open in it", name)
	}
	for _, note := range res.Relanded {
		ui.Say("kept %s", note)
	}
	for _, s := range res.Strays {
		ui.Say("dangling checkout — git lost the link; `holt <name>` moves it aside and rebuilds: %s", s)
	}
	if len(res.Reaped) == 0 {
		ui.Say("nothing to reap — every worktree is either unmerged, dirty, or in use.")
	}
	return nil
}
