// Package rootfs resolves paths and users inside an image root filesystem
// the way a container sees them, without ever leaving that root.
package rootfs

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

const maxSymlinkHops = 255

// ScopedJoin resolves p inside root the way a container sees it: symlinks are
// followed, absolute symlink targets and ".." are re-rooted at root, so the
// result never leaves root. Missing components are kept as written. It is
// used on image rootfs trees while no container runs in them.
func ScopedJoin(root, p string) (string, error) {
	root = filepath.Clean(root)
	parts := splitPath(p)
	cur := ""
	hops := 0
	for len(parts) > 0 {
		c := parts[0]
		parts = parts[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = parentRel(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(filepath.Join(root, next))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				cur = next
				continue
			}
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		hops++
		if hops > maxSymlinkHops {
			return "", fmt.Errorf("%s: too many levels of symbolic links", p)
		}
		target, err := os.Readlink(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			cur = ""
		}
		parts = append(splitPath(target), parts...)
	}
	return filepath.Join(root, cur), nil
}

func splitPath(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

func parentRel(rel string) string {
	d := filepath.Dir(rel)
	if d == "." {
		return ""
	}
	return d
}

// ResolveUser turns a USER / --chown spec ("name", "uid", "name:group",
// "uid:gid") into numeric IDs using the image's /etc/passwd and /etc/group.
// Supplementary groups of a named user are included.
func ResolveUser(rootfs, spec string) (bundle.User, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return bundle.User{}, nil
	}
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")

	passwd := readColonFile(rootfs, "/etc/passwd")
	groups := readColonFile(rootfs, "/etc/group")

	var u bundle.User
	userName := ""
	if n, err := strconv.ParseUint(userPart, 10, 32); err == nil {
		u.UID = uint32(n)
		for _, f := range passwd {
			if len(f) >= 4 && f[2] == userPart {
				userName = f[0]
				if g, err := strconv.ParseUint(f[3], 10, 32); err == nil {
					u.GID = uint32(g)
				}
				break
			}
		}
	} else {
		found := false
		for _, f := range passwd {
			if len(f) >= 4 && f[0] == userPart {
				uid, err1 := strconv.ParseUint(f[2], 10, 32)
				gid, err2 := strconv.ParseUint(f[3], 10, 32)
				if err1 != nil || err2 != nil {
					break
				}
				u.UID, u.GID, userName, found = uint32(uid), uint32(gid), f[0], true
				break
			}
		}
		if !found {
			return bundle.User{}, fmt.Errorf("unable to find user %s: no matching entries in passwd file", userPart)
		}
	}

	if hasGroup {
		gid, err := lookupGroup(groups, groupPart)
		if err != nil {
			return bundle.User{}, err
		}
		u.GID = gid
		return u, nil
	}
	if userName != "" {
		for _, f := range groups {
			if len(f) < 4 {
				continue
			}
			for _, member := range strings.Split(f[3], ",") {
				if member == userName {
					if g, err := strconv.ParseUint(f[2], 10, 32); err == nil && uint32(g) != u.GID {
						u.AdditionalGids = append(u.AdditionalGids, uint32(g))
					}
				}
			}
		}
	}
	return u, nil
}

func lookupGroup(groups [][]string, spec string) (uint32, error) {
	if n, err := strconv.ParseUint(spec, 10, 32); err == nil {
		return uint32(n), nil
	}
	for _, f := range groups {
		if len(f) >= 3 && f[0] == spec {
			if g, err := strconv.ParseUint(f[2], 10, 32); err == nil {
				return uint32(g), nil
			}
		}
	}
	return 0, fmt.Errorf("unable to find group %s: no matching entries in group file", spec)
}

// readColonFile reads an /etc/passwd-style file from the image (scoped to
// rootfs). A missing file yields no entries.
func readColonFile(rootfs, name string) [][]string {
	p, err := ScopedJoin(rootfs, name)
	if err != nil {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Split(line, ":"))
	}
	return out
}
