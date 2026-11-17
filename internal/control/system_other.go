//go:build !unix

package control

import "io/fs"

func allocated(fi fs.FileInfo, _ map[[2]uint64]bool) int64 { return fi.Size() }
