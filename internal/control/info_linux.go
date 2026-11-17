package control

import "syscall"

// freeBytes is the space available to the daemon on dir's filesystem.
func freeBytes(dir string) uint64 {
	var st syscall.Statfs_t
	if dir == "" || syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return st.Bavail * uint64(st.Bsize) //nolint:gosec // Bsize is positive
}
