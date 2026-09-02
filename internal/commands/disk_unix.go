//go:build unix

package commands

import (
	"os"
	"syscall"
)

// allocatedBytes is what `du` counts: blocks actually allocated to the file,
// not the size it reports.
//
// The difference is the entire point of measuring at all. A reflinked
// `node_modules` (SPEC.md §6.3) reports its full apparent size while sharing
// nearly every block with the tree it was cloned from, so apparent size would
// show the copy-on-write path costing exactly as much as the plain copy it
// replaced — and the disk section exists to show which of the two you got.
//
// A sparse file and a hole-punched build cache read the same way, in the same
// direction. st_blocks is 512-byte units by POSIX regardless of the
// filesystem's own block size.
func allocatedBytes(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	// No stat behind this entry (never on the platforms scruff builds for):
	// apparent size overstates a clone, which is the safe direction for a
	// number whose job is to make a big tree noticeable.
	return info.Size()
}
