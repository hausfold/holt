// Command holt manages coding-agent worktrees for any git repo.
//
// See SPEC.md for the design. The short version: holt owns the LIFECYCLE
// (create → live → parked → landed → reaped) and its safety invariants — never
// lose work, never reap something in use, keep the registry locked. What you do
// at each transition is yours.
package main

import (
	"os"

	"github.com/nebelhaus/holt/internal/commands"
	"github.com/nebelhaus/holt/internal/exitcode"
	"github.com/nebelhaus/holt/internal/ui"
)

func main() {
	err := commands.Run(os.Args[1:])
	if err != nil {
		ui.Fail(err.Error())
	}
	os.Exit(exitcode.Of(err))
}
