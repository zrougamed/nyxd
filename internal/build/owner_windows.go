package build

import "io/fs"

// ownerOf is not available on Windows.
func ownerOf(fs.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }
