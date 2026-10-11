//go:build linux

package layer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

type ent struct {
	name, link, body string
	typ              byte
	mode             int64
}

func mkTar(t *testing.T, gz bool, ents ...ent) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&buf)
		w = zw
	}
	tw := tar.NewWriter(w)
	for _, e := range ents {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
			if e.typ == tar.TypeDir {
				mode = 0o755
			}
		}
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: mode,
			Size: int64(len(e.body)), ModTime: time.Unix(1_700_000_000, 0)}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		zw.Close()
	}
	return &buf
}

// testOpts uses the user xattr namespace so tests run unprivileged.
var overlayOpts = ApplyOptions{Mode: ModeOverlay, XattrPrefix: "user."}

func isWhiteout(t *testing.T, p string) bool {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	st := fi.Sys().(*syscall.Stat_t)
	return fi.Mode()&os.ModeCharDevice != 0 && st.Rdev == 0
}

func TestApplyBasic(t *testing.T) {
	dest := t.TempDir()
	buf := mkTar(t, true,
		ent{name: "etc/", typ: tar.TypeDir},
		ent{name: "etc/hosts", typ: tar.TypeReg, body: "127.0.0.1 localhost\n"},
		ent{name: "./bin/tool", typ: tar.TypeReg, body: "#!/bin/sh\n", mode: 0o4755},
		ent{name: "etc/hosts.lnk", typ: tar.TypeLink, link: "etc/hosts"},
		ent{name: "etc/rel", typ: tar.TypeSymlink, link: "../bin/tool"},
	)
	if _, err := Apply(buf, dest, ApplyOptions{Mode: ModeFlatten}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "etc/hosts"))
	if err != nil || string(b) != "127.0.0.1 localhost\n" {
		t.Fatalf("hosts = %q, %v", b, err)
	}
	fi, _ := os.Stat(filepath.Join(dest, "bin/tool"))
	if fi.Mode()&os.ModeSetuid == 0 || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	a, _ := os.Stat(filepath.Join(dest, "etc/hosts"))
	l, _ := os.Stat(filepath.Join(dest, "etc/hosts.lnk"))
	if !os.SameFile(a, l) {
		t.Fatal("hardlink not preserved")
	}
	if got, _ := os.Readlink(filepath.Join(dest, "etc/rel")); got != "../bin/tool" {
		t.Fatalf("symlink = %q", got)
	}
	if !a.ModTime().Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("mtime = %v", a.ModTime())
	}
}

func TestApplyRejectsEscapes(t *testing.T) {
	cases := map[string][]ent{
		"symlink dir escape": {
			{name: "lnk", typ: tar.TypeSymlink, link: "/tmp"},
			{name: "lnk/evil", typ: tar.TypeReg, body: "x"},
		},
		"symlink relative escape": {
			{name: "lnk", typ: tar.TypeSymlink, link: "../../.."},
			{name: "lnk/evil", typ: tar.TypeReg, body: "x"},
		},
		"hardlink escape": {{name: "h", typ: tar.TypeLink, link: "../../etc/passwd"}},
	}
	for name, ents := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "root")
			os.Mkdir(dest, 0o755)
			_, err := Apply(mkTar(t, false, ents...), dest, ApplyOptions{Mode: ModeFlatten})
			if _, statErr := os.Stat(filepath.Join(parent, "evil")); statErr == nil {
				t.Fatal("file written outside root")
			}
			if _, statErr := os.Stat("/tmp/evil"); statErr == nil && name == "symlink dir escape" {
				t.Fatal("file written through symlink")
			}
			if strings.HasPrefix(name, "symlink") && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestApplyDotDotNormalised(t *testing.T) {
	dest := t.TempDir()
	if _, err := Apply(mkTar(t, false, ent{name: "../../evil", typ: tar.TypeReg, body: "x"}), dest, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "evil")); err != nil {
		t.Fatal("../../evil should land at <root>/evil")
	}
}

func TestApplyFollowsInRootSymlinkDirs(t *testing.T) {
	dest := t.TempDir()
	buf := mkTar(t, false,
		ent{name: "usr/lib/", typ: tar.TypeDir},
		ent{name: "lib", typ: tar.TypeSymlink, link: "usr/lib"},
		ent{name: "lib/libc.so", typ: tar.TypeReg, body: "elf"},
	)
	if _, err := Apply(buf, dest, ApplyOptions{Mode: ModeFlatten}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "usr/lib/libc.so")); err != nil {
		t.Fatal("write through in-root symlink dir did not land in usr/lib")
	}
	if fi, _ := os.Lstat(filepath.Join(dest, "lib")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("lib symlink was replaced")
	}
}

func TestApplySkipsDevicesAndOverlayXattrs(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "dev/sda", Typeflag: tar.TypeBlock, Devmajor: 8, Mode: 0o600})
	tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755, Format: tar.FormatPAX,
		PAXRecords: map[string]string{"SCHILY.xattr.user.overlay.opaque": "y", "SCHILY.xattr.user.keep": "v"}})
	tw.Close()
	if _, err := Apply(&buf, dest, overlayOpts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "dev/sda")); err == nil {
		t.Fatal("device node created")
	}
	if _, err := getXattr(filepath.Join(dest, "d"), "user.overlay.opaque"); err == nil {
		t.Fatal("overlay xattr accepted from image")
	}
	if v, err := getXattr(filepath.Join(dest, "d"), "user.keep"); err != nil || string(v) != "v" {
		t.Skipf("user xattrs unsupported on this filesystem: %v", err)
	}
}

func TestApplyOverlayWhiteouts(t *testing.T) {
	dest := t.TempDir()
	buf := mkTar(t, false,
		ent{name: "a/", typ: tar.TypeDir},
		ent{name: "a/.wh.gone", typ: tar.TypeReg},
		ent{name: "b/.wh..wh..opq", typ: tar.TypeReg},
		ent{name: "b/new", typ: tar.TypeReg, body: "n"},
		ent{name: "c/.wh..wh..plnk", typ: tar.TypeReg},
	)
	if _, err := Apply(buf, dest, overlayOpts); err != nil {
		if strings.Contains(err.Error(), "operation not supported") {
			t.Skip("user xattrs unsupported on this filesystem")
		}
		t.Fatal(err)
	}
	if !isWhiteout(t, filepath.Join(dest, "a/gone")) {
		t.Fatal("a/gone is not an overlay whiteout")
	}
	if v, err := getXattr(filepath.Join(dest, "b"), "user.overlay.opaque"); err != nil || string(v) != "y" {
		t.Fatalf("opaque xattr = %q, %v", v, err)
	}
	for _, p := range []string{"a/.wh.gone", "b/.wh..wh..opq", "c/.wh..wh..plnk"} {
		if _, err := os.Lstat(filepath.Join(dest, p)); err == nil {
			t.Fatalf("%s leaked into layer dir", p)
		}
	}
}

func TestApplyFlattenWhiteouts(t *testing.T) {
	dest := t.TempDir()
	lower := mkTar(t, false,
		ent{name: "a/keep", typ: tar.TypeReg, body: "k"},
		ent{name: "a/gone", typ: tar.TypeReg, body: "g"},
		ent{name: "b/old", typ: tar.TypeReg, body: "o"},
		ent{name: "b/sub/old", typ: tar.TypeReg, body: "o"},
	)
	if _, err := Apply(lower, dest, ApplyOptions{Mode: ModeFlatten}); err != nil {
		t.Fatal(err)
	}
	// The new entry comes before the opaque marker: it must survive.
	upper := mkTar(t, false,
		ent{name: "a/.wh.gone", typ: tar.TypeReg},
		ent{name: "b/sub/new", typ: tar.TypeReg, body: "n"},
		ent{name: "b/.wh..wh..opq", typ: tar.TypeReg},
	)
	if _, err := Apply(upper, dest, ApplyOptions{Mode: ModeFlatten}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "a/keep", "b", "b/sub", "b/sub/new"}
	if got := tree(t, dest); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tree = %v, want %v", got, want)
	}
}

func TestDecompressZstdRejected(t *testing.T) {
	_, err := Decompress(bytes.NewReader([]byte{0x28, 0xb5, 0x2f, 0xfd, 0}))
	if err == nil || !strings.Contains(err.Error(), "zstd") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiffRoundTrip(t *testing.T) {
	upper := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(upper, "etc/conf.d"), 0o755))
	must(os.WriteFile(filepath.Join(upper, "etc/conf.d/x"), []byte("x"), 0o640))
	must(os.Link(filepath.Join(upper, "etc/conf.d/x"), filepath.Join(upper, "etc/x.hard")))
	must(os.Symlink("conf.d/x", filepath.Join(upper, "etc/x.sym")))
	if err := syscall.Mknod(filepath.Join(upper, "etc/removed"), syscall.S_IFCHR, 0); err != nil {
		t.Skipf("cannot create whiteout as this user: %v", err)
	}
	if err := setXattrPlain(filepath.Join(upper, "etc/conf.d"), "user.overlay.opaque", "y"); err != nil {
		t.Skipf("user xattrs unsupported: %v", err)
	}

	var tarBuf bytes.Buffer
	epoch := time.Unix(1_000_000_000, 0)
	must(Diff(upper, &tarBuf, DiffOptions{XattrPrefix: "user.", Epoch: epoch}))

	var names []string
	tr := tar.NewReader(bytes.NewReader(tarBuf.Bytes()))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(err)
		names = append(names, h.Name)
		if h.ModTime.After(epoch) {
			t.Fatalf("%s mtime not clamped", h.Name)
		}
		if h.Name == "etc/x.hard" && h.Typeflag != tar.TypeLink {
			t.Fatalf("x.hard typeflag = %c", h.Typeflag)
		}
	}
	// Lexical walk order of the real names (whiteout sits where "removed" sorts).
	want := []string{"etc/", "etc/conf.d/", "etc/conf.d/.wh..wh..opq", "etc/conf.d/x",
		"etc/.wh.removed", "etc/x.hard", "etc/x.sym"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v\nwant      %v", names, want)
	}

	// Determinism: a second Diff yields identical bytes.
	var again bytes.Buffer
	must(Diff(upper, &again, DiffOptions{XattrPrefix: "user.", Epoch: epoch}))
	if !bytes.Equal(tarBuf.Bytes(), again.Bytes()) {
		t.Fatal("Diff is not deterministic")
	}

	// Apply the diff back as an overlay lower dir.
	dest := t.TempDir()
	if _, err := Apply(bytes.NewReader(tarBuf.Bytes()), dest, overlayOpts); err != nil {
		t.Fatal(err)
	}
	if !isWhiteout(t, filepath.Join(dest, "etc/removed")) {
		t.Fatal("whiteout lost in round trip")
	}
	if v, _ := getXattr(filepath.Join(dest, "etc/conf.d"), "user.overlay.opaque"); string(v) != "y" {
		t.Fatal("opaque lost in round trip")
	}

	// WriteBlob produces matching descriptors.
	blob := filepath.Join(t.TempDir(), "blob")
	d, err := WriteBlob(upper, blob, DiffOptions{XattrPrefix: "user.", Epoch: epoch})
	must(err)
	fi, _ := os.Stat(blob)
	if d.Size != fi.Size() || !strings.HasPrefix(d.Digest, "sha256:") || d.DiffID == d.Digest {
		t.Fatalf("bad descriptor %+v (size %d)", d, fi.Size())
	}
}

func setXattrPlain(p, k, v string) error {
	return syscall.Setxattr(p, k, []byte(v), 0)
}

func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error {
		if p != root {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}
