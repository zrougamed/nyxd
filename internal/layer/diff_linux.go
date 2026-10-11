//go:build linux

package layer

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Diff writes an uncompressed OCI layer tar of the overlayfs upper directory
// upper to w. Overlay whiteouts become ".wh." entries and opaque directories
// get a ".wh..wh..opq" marker. Entries are emitted in lexical order.
func Diff(upper string, w io.Writer, opts DiffOptions) error {
	prefix := opts.XattrPrefix
	if prefix == "" {
		prefix = DefaultXattrPrefix
	}
	upper = filepath.Clean(upper)
	tw := tar.NewWriter(w)
	links := map[uint64]string{} // inode → first path, for hardlinks

	err := filepath.WalkDir(upper, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == upper {
			return nil
		}
		rel, err := filepath.Rel(upper, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%s: no stat_t", p)
		}

		// Overlay whiteout: char device 0/0.
		if fi.Mode()&fs.ModeCharDevice != 0 && st.Rdev == 0 {
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeReg,
				Name:     path.Join(path.Dir(name), whiteoutPrefix+path.Base(name)),
				Mode:     0o600,
				ModTime:  clamp(fi.ModTime(), opts.Epoch),
				Format:   tar.FormatPAX,
			})
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Uname, hdr.Gname = "", ""
		hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
		hdr.ModTime = clamp(fi.ModTime(), opts.Epoch)
		hdr.Format = tar.FormatPAX

		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			if hdr.Linkname, err = os.Readlink(p); err != nil {
				return err
			}
		case fi.IsDir():
			hdr.Name += "/"
		case fi.Mode().IsRegular() && st.Nlink > 1:
			if first, ok := links[st.Ino]; ok {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = first
				hdr.Size = 0
			} else {
				links[st.Ino] = name
			}
		case fi.Mode()&(fs.ModeDevice|fs.ModeCharDevice) != 0:
			hdr.Devmajor = int64(unix.Major(uint64(st.Rdev)))
			hdr.Devminor = int64(unix.Minor(uint64(st.Rdev)))
		}

		if err := addXattrs(hdr, p); err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, cErr := io.CopyN(tw, f, hdr.Size)
			f.Close()
			if cErr != nil {
				return fmt.Errorf("%s: %w", name, cErr)
			}
		}

		if fi.IsDir() {
			if v, err := getXattr(p, prefix+"overlay.opaque"); err == nil && string(v) == "y" {
				return tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeReg,
					Name:     path.Join(name, whiteoutOpaque),
					Mode:     0o600,
					ModTime:  hdr.ModTime,
					Format:   tar.FormatPAX,
				})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func addXattrs(hdr *tar.Header, p string) error {
	if hdr.Typeflag == tar.TypeSymlink {
		return nil
	}
	keys, err := listXattrs(p)
	if err != nil {
		if err == unix.ENOTSUP {
			return nil
		}
		return err
	}
	for _, k := range keys {
		if skipXattr(k) {
			continue
		}
		v, err := getXattr(p, k)
		if err != nil {
			continue
		}
		if hdr.PAXRecords == nil {
			hdr.PAXRecords = map[string]string{}
		}
		hdr.PAXRecords["SCHILY.xattr."+k] = string(v)
	}
	return nil
}

func clamp(t, epoch time.Time) time.Time {
	if !epoch.IsZero() && t.After(epoch) {
		return epoch
	}
	return t
}

// WriteBlob runs Diff on upper and writes a gzip-compressed layer blob to
// dst, returning its digest, diffID and compressed size.
func WriteBlob(upper, dst string, opts DiffOptions) (Descriptor, error) {
	f, err := os.CreateTemp(filepath.Dir(dst), ".layer-*")
	if err != nil {
		return Descriptor{}, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	blobHash := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(f, blobHash)}
	gz := gzip.NewWriter(counter)
	diffHash := sha256.New()

	if err := Diff(upper, io.MultiWriter(gz, diffHash), opts); err != nil {
		f.Close()
		return Descriptor{}, err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return Descriptor{}, err
	}
	if err := f.Close(); err != nil {
		return Descriptor{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{
		Digest: "sha256:" + hex.EncodeToString(blobHash.Sum(nil)),
		DiffID: "sha256:" + hex.EncodeToString(diffHash.Sum(nil)),
		Size:   counter.n,
	}, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
