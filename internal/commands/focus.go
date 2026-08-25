package commands

import (
	"github.com/hausfold/holt/internal/config"
	"github.com/hausfold/holt/internal/exitcode"
)

// Focus is "take me to that lane" — `holt focus <name>`, or
// `holt focus <repo>/<name>` when the name lives in two repos.
//
// It is `holt <name>`'s narrower sibling, and the difference is the whole
// reason it exists as its own seam. Resume assumes the lane needs rebuilding
// and its conversation reopening, so on a machine that opens a window per lane
// it opens ANOTHER one. Focus assumes the lane is already running somewhere and
// asks the desktop to look at it — which a machine with a window manager can
// answer by raising the window it already has, and a machine without one can't
// answer at all.
//
// So the built-in is resume. A holt with no `focus` hook configured does what
// holt has always done for "go to this lane", and a consumer that knows how to
// find windows overrides exactly that step. Nothing about terminals, sessions
// or window ids enters holt either way.
//
// The caller is usually not a human: trill's `focus_lane` banner action runs
// this when a lane's banner is clicked (its ActionRouter). That is why the
// no-hook path still has to do something useful rather than fail.
func (e *Env) Focus(want string) error {
	if want == "" {
		return exitcode.Usagef("name the lane to go to: holt focus <name>  (holt, to see them)")
	}
	entry, err := e.matchLane(want)
	if err != nil {
		return err
	}

	if e.Cfg.Defined(config.HookFocus) {
		payload := e.hookPayload(entry.Main, entry.Branch, entry.Path, e.agentForPath(entry.Path))
		payload["state"] = string(entry.State)
		res := e.Cfg.Do(config.HookFocus, payload)
		e.noteHook(res)
		if res.Answer != config.Defer {
			return hookOutcome(config.HookFocus, res)
		}
		// Deferred: the window layer looked and found nothing of this lane to
		// raise. Falling through to resume is what makes that honest — a lane
		// with no window is detached, not gone, and opening one onto it is the
		// answer to the click.
	}
	return e.Resume(want, false)
}
