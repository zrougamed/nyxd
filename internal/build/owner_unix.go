//go:build !windows

package build

import (
	"io/fs"
	"syscall"
)

// ownerOf returns the uid/gid recorded in fi.
func ownerOf(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
