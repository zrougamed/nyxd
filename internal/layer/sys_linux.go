//go:build linux

package layer

import (
	"fmt"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// withParent opens the parent directory of name inside root and calls fn with
// its fd and the final path component. Operating on (dirfd, base) keeps the
// final component from being resolved through a symlink outside the root.
func withParent(root *os.Root, name string, fn func(dirfd int, base string) error) error {
	dir, base := path.Dir(name), path.Base(name)
	if name == "" || name == "." {
		dir, base = ".", "."
	}
	d, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return fn(int(d.Fd()), base)
}

func mkWhiteoutAt(root *os.Root, name string) error {
	return withParent(root, name, func(dirfd int, base string) error {
		return unix.Mknodat(dirfd, base, unix.S_IFCHR, 0)
	})
}

func mkfifoAt(root *os.Root, name string, perm uint32) error {
	return withParent(root, name, func(dirfd int, base string) error {
		return unix.Mkfifoat(dirfd, base, perm)
	})
}

// setXattrAt sets an xattr on name without following a final symlink. The
// /proc/self/fd/<dirfd>/<base> form lets lsetxattr work on any file type
// (including directories and files we cannot open for reading).
func setXattrAt(root *os.Root, name, key string, value []byte) error {
	return withParent(root, name, func(dirfd int, base string) error {
		return unix.Lsetxattr(fmt.Sprintf("/proc/self/fd/%d/%s", dirfd, base), key, value, 0)
	})
}

func getXattr(p, key string) ([]byte, error) {
	buf := make([]byte, 256)
	for {
		n, err := unix.Lgetxattr(p, key, buf)
		if err == unix.ERANGE {
			buf = make([]byte, len(buf)*4)
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
}

func listXattrs(p string) ([]string, error) {
	sz, err := unix.Llistxattr(p, nil)
	if err != nil || sz == 0 {
		return nil, err
	}
	buf := make([]byte, sz)
	sz, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, err
	}
	var keys []string
	start := 0
	for i := 0; i < sz; i++ {
		if buf[i] == 0 {
			if i > start {
				keys = append(keys, string(buf[start:i]))
			}
			start = i + 1
		}
	}
	return keys, nil
}
