package build

import (
	"bufio"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/build/dockerfile"
	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/layer"
	"github.com/zrougamed/nyxd/internal/rootfs"
)

// copySource is one resolved COPY/ADD source.
type copySource struct {
	abs     string // path on disk
	rel     string // as written, for messages
	isDir   bool
	url     string // ADD <url>: downloaded to abs (temp file)
	archive bool   // ADD of a local tar archive: extract instead of copy
}

type copyOpts struct {
	chown    *bundle.User // nil: default ownership
	keepOwn  bool         // COPY --from keeps source ownership
	chmod    *os.FileMode
	singleID bool
}

func (bd *build) copy(ctx context.Context, st *stageState, in dockerfile.Instruction) error {
	isAdd := in.Cmd == "add"
	var fromRef, chownSpec, chmodSpec, checksum string
	for _, f := range in.Flags {
		name, val, _ := strings.Cut(strings.TrimPrefix(f, "--"), "=")
		switch name {
		case "from":
			if isAdd {
				return errors.New("ADD does not support --from")
			}
			fromRef = val
		case "chown":
			chownSpec = val
		case "chmod":
			chmodSpec = val
		case "checksum":
			checksum = val
		case "link":
			// Layer is independent of its parent by construction here; nothing to do.
		default:
			return fmt.Errorf("unsupported flag %s", f)
		}
	}

	var words []string
	var err error
	if in.JSON {
		words = in.Args
	} else {
		words, err = dockerfile.Words(in.Raw, bd.df.Escape, st.lookup)
		if err != nil {
			return err
		}
	}
	if len(words) < 2 {
		return errors.New("requires at least one source and a destination")
	}
	srcs, dest := words[:len(words)-1], words[len(words)-1]
	if chownSpec, err = dockerfile.Expand(chownSpec, bd.df.Escape, st.lookup); err != nil {
		return err
	}

	b := bd.b
	// Source root: the build context, or a stage / image for COPY --from.
	srcRoot := bd.opts.ContextDir
	opts := copyOpts{singleID: b.cfg.SingleID}
	fromKey := "context"
	if fromRef != "" {
		fromRef, err = dockerfile.Expand(fromRef, bd.df.Escape, st.lookup)
		if err != nil {
			return err
		}
		from, err := bd.baseState(ctx, fromRef)
		if err != nil {
			return fmt.Errorf("--from=%s: %w", fromRef, err)
		}
		id := randomID("nyxfrom-")
		merged, err := b.cfg.Overlay.Prepare(id, b.blobPaths(from.layers), nil)
		if err != nil {
			return err
		}
		defer b.cfg.Overlay.Remove(id) //nolint:errcheck
		srcRoot = merged
		opts.keepOwn = true
		fromKey = "from:" + from.key
	}
	if chmodSpec != "" {
		n, err := strconv.ParseUint(chmodSpec, 8, 32)
		if err != nil || n > 0o7777 {
			return fmt.Errorf("--chmod=%s: expected an octal mode", chmodSpec)
		}
		m := octalMode(uint32(n))
		opts.chmod = &m
	}

	var tmpFiles []string
	defer func() {
		for _, f := range tmpFiles {
			os.Remove(f)
		}
	}()
	var sources []copySource
	for _, s := range srcs {
		if isAdd && isURL(s) {
			cs, err := bd.download(ctx, s, checksum)
			if err != nil {
				return err
			}
			tmpFiles = append(tmpFiles, cs.abs)
			sources = append(sources, cs)
			continue
		}
		if isURL(s) {
			return fmt.Errorf("%s: COPY does not take URLs (use ADD)", s)
		}
		if strings.HasPrefix(s, "git@") {
			return fmt.Errorf("%s: git sources are not supported", s)
		}
		matched, err := expandSource(srcRoot, s)
		if err != nil {
			return err
		}
		for _, m := range matched {
			if isAdd && !m.isDir && isArchive(m.abs) {
				m.archive = true
			}
			sources = append(sources, m)
		}
	}
	if checksum != "" && !(isAdd && len(srcs) == 1 && isURL(srcs[0])) {
		return errors.New("--checksum is only supported for a single ADD <url>")
	}

	// Destination: relative to WORKDIR; a directory when it ends with "/",
	// or with several sources.
	destDir := strings.HasSuffix(dest, "/") || len(sources) > 1
	if !path.IsAbs(dest) {
		wd := st.cfg.Config.WorkingDir
		if wd == "" {
			wd = "/"
		}
		dest = path.Join(wd, dest)
	}
	dest = path.Clean(dest)

	sum, err := hashSources(sources, opts.keepOwn)
	if err != nil {
		return err
	}
	st.key = chainKey(st.key, in.Cmd, fromKey, sum, dest, strconv.FormatBool(destDir),
		chownSpec, chmodSpec, st.cfg.Config.WorkingDir)
	createdBy := fmt.Sprintf("%s %s in %s", strings.ToUpper(in.Cmd), sum[:12], dest)
	if !bd.opts.NoCache {
		if e, ok := b.cache.get(st.key, b.cfg.Store.HasBlob); ok {
			fmt.Fprintln(bd.out, "--> Using cache")
			st.addLayer(e, createdBy)
			return nil
		}
	}

	id := randomID("nyxcopy-")
	merged, err := b.cfg.Overlay.Prepare(id, b.blobPaths(st.layers), nil)
	if err != nil {
		return err
	}
	defer b.cfg.Overlay.Remove(id) //nolint:errcheck

	if chownSpec != "" {
		u, err := rootfs.ResolveUser(merged, chownSpec)
		if err != nil {
			return fmt.Errorf("--chown=%s: %w", chownSpec, err)
		}
		opts.chown = &u
	}

	// An existing directory at dest also makes it a directory destination.
	if p, err := rootfs.ScopedJoin(merged, dest); err == nil {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			destDir = true
		}
	}
	for _, s := range sources {
		if err := copyInto(merged, s, dest, destDir, opts); err != nil {
			return fmt.Errorf("%s: %w", s.rel, err)
		}
	}

	if err := b.cfg.Overlay.Unmount(id); err != nil {
		return err
	}
	desc, diffID, err := b.commitLayer(id)
	if err != nil {
		return err
	}
	e := cacheEntry{Layer: desc, DiffID: diffID}
	if err := b.cache.put(st.key, e); err != nil {
		b.cfg.Log.Warn("build cache write", "err", err)
	}
	st.addLayer(e, createdBy)
	return nil
}

func octalMode(n uint32) os.FileMode {
	m := os.FileMode(n & 0o777)
	if n&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if n&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if n&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// expandSource resolves a source pattern inside root. Wildcards are matched
// per path segment; every match is resolved with scopedJoin so symlinks
// cannot lead outside root.
func expandSource(root, pattern string) ([]copySource, error) {
	clean := strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(pattern)), "/")
	if clean == "" {
		clean = "."
	}
	var matches []string
	if strings.ContainsAny(clean, "*?[") {
		var err error
		matches, err = fs.Glob(os.DirFS(root), clean)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("%s: no source files were specified", pattern)
		}
	} else {
		matches = []string{clean}
	}
	out := make([]copySource, 0, len(matches))
	for _, m := range matches {
		abs, err := rootfs.ScopedJoin(root, m)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%s: not found in build context", pattern)
			}
			return nil, err
		}
		out = append(out, copySource{abs: abs, rel: m, isDir: fi.IsDir()})
	}
	return out, nil
}

// hashSources fingerprints the source content (paths, modes, sizes, data,
// symlink targets and, for --from, ownership) for the cache key.
func hashSources(sources []copySource, withOwner bool) (string, error) {
	h := sha256.New()
	for _, s := range sources {
		fmt.Fprintf(h, "src %s %s %v %v\n", s.rel, s.url, s.isDir, s.archive)
		err := filepath.WalkDir(s.abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			fi, err := os.Lstat(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(s.abs, p)
			fmt.Fprintf(h, "%s %o %d", filepath.ToSlash(rel), fi.Mode(), fi.Size())
			if withOwner {
				if uid, gid, ok := ownerOf(fi); ok {
					fmt.Fprintf(h, " %d:%d", uid, gid)
				}
			}
			switch {
			case fi.Mode()&fs.ModeSymlink != 0:
				t, _ := os.Readlink(p)
				fmt.Fprintf(h, " -> %s", t)
			case fi.Mode().IsRegular():
				if err := hashFile(h, p); err != nil {
					return err
				}
			}
			h.Write([]byte{'\n'})
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(h hash.Hash, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(h, f)
	return err
}

// copyInto copies one source into the merged rootfs at dest.
func copyInto(merged string, s copySource, dest string, destDir bool, o copyOpts) error {
	switch {
	case s.archive:
		target, err := mkdirScoped(merged, dest, o)
		if err != nil {
			return err
		}
		return extractArchive(s.abs, target, o)
	case s.isDir:
		target, err := mkdirScoped(merged, dest, o)
		if err != nil {
			return err
		}
		return copyTree(s.abs, target, merged, o)
	default:
		target := dest
		if destDir {
			name := path.Base(filepath.ToSlash(s.rel))
			if s.url != "" {
				name = urlFileName(s.url)
			}
			target = path.Join(dest, name)
		}
		parent, err := mkdirScoped(merged, path.Dir(target), o)
		if err != nil {
			return err
		}
		return copyEntry(s.abs, filepath.Join(parent, path.Base(target)), merged, o, s.url != "")
	}
}

// mkdirScoped creates dir (and parents, owned by root, 0755) inside merged
// and returns its resolved path.
func mkdirScoped(merged, dir string, o copyOpts) (string, error) {
	p, err := rootfs.ScopedJoin(merged, dir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(p); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("%s exists and is not a directory", dir)
		}
		return p, nil
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	if o.chown != nil && !o.singleID {
		_ = os.Lchown(p, int(o.chown.UID), int(o.chown.GID))
	}
	return p, nil
}

// copyTree copies the contents of srcDir into dstDir (a resolved path inside
// merged). Each destination's parent is resolved through the image's own
// symlinks (scoped to merged), and a source directory landing on a symlink
// to a directory goes through it, so merged-/usr layouts (lib -> usr/lib)
// stay intact.
func copyTree(srcDir, dstDir, merged string, o copyOpts) error {
	baseRel, err := filepath.Rel(merged, dstDir)
	if err != nil || !within(merged, dstDir) {
		return fmt.Errorf("destination outside the image root")
	}
	type dirTime struct {
		dst   string
		mtime time.Time
	}
	var dirs []dirTime
	err = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == srcDir {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, p)
		parent, err := rootfs.ScopedJoin(merged, filepath.Join(baseRel, filepath.Dir(rel)))
		if err != nil {
			return err
		}
		dst := filepath.Join(parent, filepath.Base(rel))
		if d.IsDir() {
			if fi, err := os.Lstat(dst); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				if resolved, err := rootfs.ScopedJoin(merged, filepath.Join(baseRel, rel)); err == nil {
					if rfi, err := os.Stat(resolved); err == nil && rfi.IsDir() {
						dst = resolved
					}
				}
			}
			if fi, err := os.Lstat(p); err == nil {
				dirs = append(dirs, dirTime{dst, fi.ModTime()})
			}
		}
		return copyEntry(p, dst, merged, o, false)
	})
	if err != nil {
		return err
	}
	// Directory mtimes last, after their contents were written.
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chtimes(dirs[i].dst, dirs[i].mtime, dirs[i].mtime)
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// copyEntry copies one file system object from src to dst (dst's parent
// exists). Existing non-directories at dst are replaced.
func copyEntry(src, dst, merged string, o copyOpts, downloaded bool) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if cur, err := os.Lstat(dst); err == nil {
		if !(cur.IsDir() && fi.IsDir()) {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
	}

	mode := fi.Mode()
	switch {
	case mode.IsDir():
		if err := os.Mkdir(dst, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
	case mode.IsRegular():
		if err := copyFileData(src, dst); err != nil {
			return err
		}
	default:
		return nil // sockets, devices, fifos are not copied
	}

	uid, gid := 0, 0
	if o.keepOwn {
		if u, g, ok := ownerOf(fi); ok {
			uid, gid = u, g
		}
	}
	if o.chown != nil {
		uid, gid = int(o.chown.UID), int(o.chown.GID)
	}
	if o.singleID {
		uid, gid = 0, 0
	}
	// The daemon is root (in its namespace when rootless); only unprivileged
	// test runs skip ownership.
	if os.Geteuid() == 0 {
		if err := os.Lchown(dst, uid, gid); err != nil {
			return err
		}
	}
	if mode&fs.ModeSymlink != 0 {
		return nil
	}
	perm := mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	if downloaded {
		perm = 0o600
	}
	if o.chmod != nil {
		perm = *o.chmod
	}
	if err := os.Chmod(dst, perm); err != nil {
		return err
	}
	if !mode.IsDir() {
		return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}
	return nil
}

func copyFileData(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, cErr := io.Copy(out, in)
	if err := out.Close(); cErr == nil {
		cErr = err
	}
	return cErr
}

// isArchive reports whether p is a gzip, bzip2 or plain tar archive.
func isArchive(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return true
	case len(head) >= 3 && string(head[:3]) == "BZh":
		return true
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return true
	}
	return false
}

func extractArchive(src, target string, o copyOpts) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var r io.Reader = br
	if head, _ := br.Peek(3); string(head) == "BZh" {
		r = bzip2.NewReader(br)
	}
	_, err = layer.Apply(r, target, layer.ApplyOptions{Mode: layer.ModeFlatten, Chown: !o.singleID && os.Geteuid() == 0})
	return err
}

func urlFileName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "download"
	}
	name := path.Base(u.Path)
	if name == "/" || name == "." || name == "" {
		return "download"
	}
	return name
}

// download fetches an ADD <url> into a temp file and verifies --checksum.
func (bd *build) download(ctx context.Context, raw, checksum string) (copySource, error) {
	tmpDir, err := bd.b.cfg.Store.TempDir()
	if err != nil {
		return copySource{}, err
	}
	fmt.Fprintf(bd.out, "downloading %s\n", raw)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return copySource{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return copySource{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return copySource{}, fmt.Errorf("%s: %s", raw, resp.Status)
	}
	f, err := os.CreateTemp(tmpDir, "add-")
	if err != nil {
		return copySource{}, err
	}
	h := sha256.New()
	_, cErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	if err := f.Close(); cErr == nil {
		cErr = err
	}
	if cErr != nil {
		os.Remove(f.Name())
		return copySource{}, cErr
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if checksum != "" && checksum != got {
		os.Remove(f.Name())
		return copySource{}, fmt.Errorf("%s: checksum mismatch: want %s, got %s", raw, checksum, got)
	}
	if lm, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		_ = os.Chtimes(f.Name(), lm, lm)
	}
	return copySource{abs: f.Name(), rel: raw, url: raw}, nil
}
