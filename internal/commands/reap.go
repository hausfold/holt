package commands

import (
	"github.com/hausfold/scruff/internal/ui"
)

// Reap sweeps every LANDED lane now — parked ones, plus clean, landed checkouts
// that NO pane is sitting in.
//
// This is the idempotent backstop for when a pane ends WITHOUT firing the remove
// hook (a manual close, a reboot, a crash), and for `scruff child` checkouts,
// which the hook never reaps.
func (e *Env) Reap() error {
	// A sweep that DELETES branches must ask the forge fresh. The listing's
	// 2-minute memo is right for an annotation and wrong here: a PR merged 30
	// seconds ago should reap on this run, and a PR reopened 30 seconds ago must
	// not.
	cacheTTL = 0

	res := e.reapSweep(sweepAll)

	// res.Degraded needs no line of its own — Env.Warn already said it out loud
	// on the way past, and saying it twice reads as two different problems.
	for _, name := range res.Reaped {
		ui.Say("reaped %s", name)
	}
	for _, note := range res.SkippedLive {
		ui.Say("kept %s", note)
	}
	for _, note := range res.Dirty {
		ui.Say("kept %s", note)
	}
	for _, note := range res.Relanded {
		ui.Say("kept %s", note)
	}
	for _, note := range res.Diverged {
		ui.Say("kept %s", note)
	}
	for _, note := range res.DeadEnds {
		ui.Say("kept %s", note)
	}
	for _, s := range res.Strays {
		ui.Say("dangling checkout — git lost the link; `scruff <name>` moves it aside and rebuilds: %s", s)
	}
	if len(res.Reaped) == 0 {
		// Only when nothing above spoke. The old unconditional line listed the
		// three reasons in the abstract right after naming the concrete one,
		// which read as a second, contradictory verdict.
		spoke := len(res.SkippedLive) + len(res.Dirty) + len(res.Relanded) +
			len(res.Diverged) + len(res.DeadEnds) + len(res.Strays)
		if spoke == 0 {
			ui.Say("nothing to reap — every lane is either unmerged, dirty, or in use.")
		} else {
			ui.Say("nothing reaped — see above for what held each lane back.")
		}
	}
	return nil
}
