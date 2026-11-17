package layer

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// Owner sets the uid and gid recorded for every entry.
type Owner struct{ UID, GID int }

// WriteTree writes rel (a file, symlink or directory tree inside root) to w
// as a tar stream whose top-level entry is called name. Symlinks are stored,
// not followed, and every read goes through root, so nothing outside it is
// reachable. Hard links are stored as regular files; sockets are skipped.
// A non-nil owner replaces the files' own uid and gid.
func WriteTree(w io.Writer, root *os.Root, rel, name string, owner *Owner) error {
	tw := tar.NewWriter(w)
	if err := writeEntry(tw, root, cleanRel(rel), strings.Trim(name, "/"), owner); err != nil {
		return err
	}
	return tw.Close()
}

func cleanRel(rel string) string {
	if n := cleanName(rel); n != "" {
		return n
	}
	return "."
}

func writeEntry(tw *tar.Writer, root *os.Root, rel, name string, owner *Owner) error {
	fi, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	link := ""
	if fi.Mode()&fs.ModeSymlink != 0 {
		if link, err = root.Readlink(rel); err != nil {
			return err
		}
	}
	if fi.Mode()&fs.ModeSocket != 0 {
		return nil
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	hdr.Name = name
	hdr.Uname, hdr.Gname = "", "" // ids only: host names mean nothing in a container
	hdr.Format = tar.FormatPAX
	if owner != nil {
		hdr.Uid, hdr.Gid = owner.UID, owner.GID
	}
	if fi.IsDir() {
		hdr.Name += "/"
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	switch {
	case fi.Mode().IsRegular():
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		_, cErr := io.CopyN(tw, f, hdr.Size)
		f.Close()
		if cErr != nil {
			return fmt.Errorf("%s: %w", rel, cErr)
		}
	case fi.IsDir():
		d, err := root.Open(rel)
		if err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, n := range names {
			if err := writeEntry(tw, root, path.Join(rel, n), name+"/"+n, owner); err != nil {
				return err
			}
		}
	}
	return nil
}

// RenameTop returns r with the first path component of every entry
// replaced by name (and hard link targets with it), for copying a tree to a
// destination with a different name.
func RenameTop(r io.Reader, name string) io.Reader {
	name = strings.Trim(name, "/")
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(renameTop(r, pw, name))
	}()
	return pr
}

func renameTop(r io.Reader, w io.Writer, name string) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	rename := func(n string) string {
		c := cleanName(n)
		if c == "" {
			return c
		}
		if _, rest, ok := strings.Cut(c, "/"); ok {
			return name + "/" + rest
		}
		return name
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return tw.Close()
		}
		if err != nil {
			return err
		}
		dir := strings.HasSuffix(hdr.Name, "/")
		hdr.Name = rename(hdr.Name)
		if hdr.Name == "" {
			continue
		}
		if dir {
			hdr.Name += "/"
		}
		if hdr.Typeflag == tar.TypeLink {
			hdr.Linkname = rename(hdr.Linkname)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}
