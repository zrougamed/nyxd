//go:build unix

package control

import (
	"io/fs"
	"syscall"
)

// allocated is the space fi takes on disk; a hard link seen before is free.
func allocated(fi fs.FileInfo, seen map[[2]uint64]bool) int64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size()
	}
	if st.Nlink > 1 {
		key := [2]uint64{uint64(st.Dev), st.Ino} //nolint:unconvert // Dev is uint64 or int32 per OS
		if seen[key] {
			return 0
		}
		seen[key] = true
	}
	return st.Blocks * 512
}
