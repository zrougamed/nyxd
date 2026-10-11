// Package layer applies and creates OCI image layers in-process (no host tar).
//
// Apply extracts a layer tarball into a directory, either as an overlayfs lower
// directory (whiteouts become overlay markers) or flattened onto an existing
// tree (whiteouts become deletes). Diff turns an overlayfs upper directory back
// into an OCI layer tarball. All writes go through os.Root so entries cannot
// escape the destination via "..", absolute paths or planted symlinks.
package layer

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// Mode selects how OCI whiteouts are materialised by Apply.
type Mode int

const (
	// ModeOverlay writes whiteouts as overlayfs markers (char 0/0 device,
	// opaque xattr) so the directory can be used as an overlay lowerdir.
	ModeOverlay Mode = iota
	// ModeFlatten applies whiteouts as real deletions on top of dest.
	ModeFlatten
)

const (
	whiteoutPrefix = ".wh."
	whiteoutMeta   = ".wh..wh."
	whiteoutOpaque = ".wh..wh..opq"

	// DefaultXattrPrefix is the overlayfs xattr namespace for a rootful mount.
	// Rootless mounts with "userxattr" use "user.".
	DefaultXattrPrefix = "trusted."
)

// ApplyOptions controls Apply.
type ApplyOptions struct {
	Mode Mode
	// XattrPrefix is "trusted." (default) or "user." (overlay userxattr).
	XattrPrefix string
	// Chown applies the uid/gid recorded in the tar headers.
	Chown bool
	// IDMap optionally remaps tar uid/gid before chown (rootless).
	IDMap func(uid, gid int) (int, int)
}

func (o ApplyOptions) xattrPrefix() string {
	if o.XattrPrefix == "" {
		return DefaultXattrPrefix
	}
	return o.XattrPrefix
}

// Apply extracts the (optionally gzip-compressed) layer read from r into dest.
// dest must exist. It returns the number of uncompressed tar bytes read.
func Apply(r io.Reader, dest string, opts ApplyOptions) (int64, error) {
	dr, err := Decompress(r)
	if err != nil {
		return 0, err
	}
	defer dr.Close()

	root, err := os.OpenRoot(dest)
	if err != nil {
		return 0, fmt.Errorf("open layer root: %w", err)
	}
	defer root.Close()

	cr := &countingReader{r: dr}
	a := &applier{root: root, opts: opts, written: map[string]struct{}{}}
	tr := tar.NewReader(cr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return cr.n, fmt.Errorf("tar next: %w", err)
		}
		if err := a.entry(tr, hdr); err != nil {
			return cr.n, fmt.Errorf("entry %q: %w", hdr.Name, err)
		}
	}
	if err := a.finish(); err != nil {
		return cr.n, err
	}
	// Drain trailing padding so callers hashing the stream see every byte.
	_, _ = io.Copy(io.Discard, cr)
	return cr.n, nil
}

// Decompress sniffs gzip / zstd / plain tar and returns a tar stream.
func Decompress(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read layer header: %w", err)
	}
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		return gr, nil
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return nil, errors.New("zstd-compressed layers are not supported yet")
	default:
		return io.NopCloser(br), nil
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type dirTime struct {
	name  string
	mtime time.Time
}

type applier struct {
	root     *os.Root
	opts     ApplyOptions
	written  map[string]struct{} // paths created by this layer (flatten opaque handling)
	opaques  []string            // dirs marked opaque in flatten mode, processed at the end
	dirTimes []dirTime
}

// cleanName normalises a tar entry name to a root-relative slash path.
// It returns "" for the root itself.
// cleanName normalises a tar entry name to a root-relative slash path, the
// way Docker does: it is cleaned against "/", so "../x" becomes "x". It
// returns "" for the root itself.
func cleanName(name string) string {
	n := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	if n == "." {
		return ""
	}
	return n
}

func (a *applier) markWritten(name string) {
	for n := name; n != "." && n != ""; n = path.Dir(n) {
		a.written[n] = struct{}{}
	}
}

func (a *applier) entry(r io.Reader, hdr *tar.Header) error {
	name := cleanName(hdr.Name)
	if name == "" {
		return nil
	}
	dir, base := path.Dir(name), path.Base(name)

	if strings.HasPrefix(base, whiteoutPrefix) {
		return a.whiteout(dir, base)
	}

	if dir != "." {
		if err := a.ensureDir(dir); err != nil {
			return err
		}
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		if fi, err := a.root.Lstat(name); err == nil && !fi.IsDir() {
			if err := a.root.RemoveAll(name); err != nil {
				return err
			}
		}
		if err := a.root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		a.dirTimes = append(a.dirTimes, dirTime{name, hdr.ModTime})

	case tar.TypeReg, tar.TypeRegA:
		if err := a.replace(name); err != nil {
			return err
		}
		f, err := a.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, cErr := io.Copy(f, r)
		if err := f.Close(); cErr == nil {
			cErr = err
		}
		if cErr != nil {
			return cErr
		}

	case tar.TypeSymlink:
		if err := a.replace(name); err != nil {
			return err
		}
		if err := a.root.Symlink(hdr.Linkname, name); err != nil {
			return err
		}

	case tar.TypeLink:
		target := cleanName(hdr.Linkname)
		if target == "" {
			return fmt.Errorf("hardlink target rejected: %q", hdr.Linkname)
		}
		if err := a.replace(name); err != nil {
			return err
		}
		if err := a.root.Link(target, name); err != nil {
			return err
		}
		a.markWritten(name)
		return nil // a hardlink shares metadata with its target

	case tar.TypeFifo:
		if err := a.replace(name); err != nil {
			return err
		}
		if err := mkfifoAt(a.root, name, uint32(hdr.Mode&0o7777)); err != nil {
			return err
		}

	default:
		// Device nodes (and unknown types) are never created from image layers.
		return nil
	}

	a.markWritten(name)
	return a.metadata(name, hdr)
}

// replace removes whatever currently occupies name (except a directory being
// re-declared, handled by the caller).
func (a *applier) replace(name string) error {
	if _, err := a.root.Lstat(name); err == nil {
		return a.root.RemoveAll(name)
	}
	return nil
}

// ensureDir makes dir exist. Symlinks along the way are followed as long as
// they stay inside the root (merged-/usr layouts like lib -> usr/lib); a
// symlink escaping the root makes os.Root fail the operation.
func (a *applier) ensureDir(dir string) error {
	fi, err := a.root.Stat(dir)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		if err := a.root.Remove(dir); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return a.root.MkdirAll(dir, 0o755)
}

func (a *applier) metadata(name string, hdr *tar.Header) error {
	isLink := hdr.Typeflag == tar.TypeSymlink
	if a.opts.Chown {
		uid, gid := hdr.Uid, hdr.Gid
		if a.opts.IDMap != nil {
			uid, gid = a.opts.IDMap(uid, gid)
		}
		if err := a.root.Lchown(name, uid, gid); err != nil {
			return err
		}
	}
	if isLink {
		return nil
	}
	// chmod after chown: chown clears setuid/setgid.
	if err := a.root.Chmod(name, tarMode(hdr.Mode)); err != nil {
		return err
	}
	if err := a.xattrs(name, hdr); err != nil {
		return err
	}
	if hdr.Typeflag != tar.TypeDir {
		return a.root.Chtimes(name, hdr.ModTime, hdr.ModTime)
	}
	return nil
}

func tarMode(m int64) os.FileMode {
	mode := os.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

// skipXattr reports xattrs that must never be taken from an image: overlay
// control attributes (an image could otherwise forge whiteouts/redirects) and
// host-specific SELinux labels.
func skipXattr(key string) bool {
	return strings.HasPrefix(key, "trusted.") ||
		strings.HasPrefix(key, "user.overlay.") ||
		key == "security.selinux"
}

func (a *applier) xattrs(name string, hdr *tar.Header) error {
	for k, v := range hdr.PAXRecords {
		key, ok := strings.CutPrefix(k, "SCHILY.xattr.")
		if !ok || skipXattr(key) {
			continue
		}
		if err := setXattrAt(a.root, name, key, []byte(v)); err != nil {
			return fmt.Errorf("xattr %s: %w", key, err)
		}
	}
	return nil
}

func (a *applier) whiteout(dir, base string) error {
	if dir != "." {
		if err := a.ensureDir(dir); err != nil {
			return err
		}
	}
	if base == whiteoutOpaque {
		if a.opts.Mode == ModeFlatten {
			a.opaques = append(a.opaques, dir)
			return nil
		}
		return setXattrAt(a.root, dir, a.opts.xattrPrefix()+"overlay.opaque", []byte("y"))
	}
	if strings.HasPrefix(base, whiteoutMeta) {
		return nil // other AUFS metadata (.wh..wh..plnk etc.)
	}
	target := path.Join(dir, strings.TrimPrefix(base, whiteoutPrefix))
	if target == "." || strings.Contains(strings.TrimPrefix(base, whiteoutPrefix), "/") {
		return fmt.Errorf("invalid whiteout")
	}
	if err := a.replace(target); err != nil {
		return err
	}
	if a.opts.Mode == ModeFlatten {
		return nil
	}
	return mkWhiteoutAt(a.root, target)
}

func (a *applier) finish() error {
	for _, dir := range a.opaques {
		if err := a.pruneOpaque(dir); err != nil {
			return fmt.Errorf("opaque %q: %w", dir, err)
		}
	}
	for i := len(a.dirTimes) - 1; i >= 0; i-- {
		dt := a.dirTimes[i]
		if err := a.root.Chtimes(dt.name, dt.mtime, dt.mtime); err != nil {
			return err
		}
	}
	return nil
}

// pruneOpaque removes everything under dir that this layer did not create.
func (a *applier) pruneOpaque(dir string) error {
	f, err := a.root.Open(dir)
	if err != nil {
		return err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		p := path.Join(dir, n)
		if _, ok := a.written[p]; !ok {
			if err := a.root.RemoveAll(p); err != nil {
				return err
			}
			continue
		}
		if fi, err := a.root.Lstat(p); err == nil && fi.IsDir() {
			if err := a.pruneOpaque(p); err != nil {
				return err
			}
		}
	}
	return nil
}
