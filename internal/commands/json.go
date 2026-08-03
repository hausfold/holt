package commands

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nebelhaus/holt/internal/gitx"
)

// The --json envelope is a frozen public contract (SPEC.md §2.2): `bench`, the
// nebelhaus statusline and pounce's Spawn Agent command all pin it within a day
// of cutover. Field ADDITIONS are non-breaking and consumers must ignore unknown
// keys; removals and meaning changes are not.
//
// The nullable fields are the part that matters. `occupied`, `dirty` and `pr`
// are pointers so that "not determined" (no lsof, no forge, cache miss) is
// distinguishable from "false". Every consumer bug in the shell version's
// statusline came from conflating those two.

type jsonEnvelope struct {
	Holt      string         `json:"holt"`
	Schema    int            `json:"schema"`
	Worktrees []jsonWorktree `json:"worktrees"`
	Warnings  []string       `json:"warnings"`
}

type jsonWorktree struct {
	Name           string        `json:"name"`
	Repo           string        `json:"repo"`
	Main           string        `json:"main"`
	Branch         string        `json:"branch"`
	Path           string        `json:"path"`
	Parent         string        `json:"parent"`
	Agent          string        `json:"agent"`
	State          string        `json:"state"`
	Occupied       *bool         `json:"occupied"`
	Dirty          *bool         `json:"dirty"`
	Landed         jsonLanded    `json:"landed"`
	PostMergeAhead jsonPostMerge `json:"post_merge_ahead"`
	Last           string        `json:"last_commit"`
}

type jsonLanded struct {
	Verdict    string `json:"verdict"` // yes | no | contained
	Via        string `json:"via"`
	Confidence string `json:"confidence"`
}

type jsonPostMerge struct {
	Commits int `json:"commits"`
	PR      int `json:"pr"`
}

func (e *Env) listJSON(rows []listRow) error {
	occupied, occKnown := occupancy()

	out := jsonEnvelope{
		Holt:      Version,
		Schema:    1,
		Worktrees: []jsonWorktree{},
		Warnings:  []string{},
	}
	for _, r := range rows {
		entry := r.Entry
		slug, err := gitx.RemoteSlug(entry.Main)
		if err != nil {
			slug = "local/" + filepath.Base(entry.Main)
		}
		w := jsonWorktree{
			Name:           r.Name,
			Repo:           slug,
			Main:           entry.Main,
			Branch:         entry.Branch,
			Path:           entry.Path,
			Agent:          r.Agent,
			State:          string(entry.State),
			Last:           r.Last,
			PostMergeAhead: jsonPostMerge{Commits: r.Ahead, PR: r.AheadPR},
		}
		if row, ok := e.Reg.Find(entry.Path); ok {
			w.Parent = row.Parent
		}
		if occKnown {
			occ := isOccupied(occupied, entry.Path)
			w.Occupied = &occ
		}
		if entry.State == Live {
			dirty := gitx.Dirty(entry.Path)
			w.Dirty = &dirty
		}
		v := e.Landed(entry.Main, entry.Branch)
		w.Landed = jsonLanded{Verdict: "no", Via: v.Via, Confidence: v.Confidence}
		switch {
		case v.Landed:
			w.Landed.Verdict = "yes"
		case v.Via == "merge-tree-empty":
			// Advisory only: this cannot tell a squash merge from a branch that
			// never did anything, so `reap` ignores it without --contained.
			w.Landed.Verdict = "contained"
		}
		out.Worktrees = append(out.Worktrees, w)
	}
	out.Warnings = append(out.Warnings, e.Warnings...)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
