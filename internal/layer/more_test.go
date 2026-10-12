//go:build linux

package layer

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestApplySpecialEntries(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	mt := time.Unix(1_700_000_000, 0)
	hdrs := []*tar.Header{
		{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: mt, Size: 1,
			PAXRecords: map[string]string{"SCHILY.xattr.user.k": "v", "SCHILY.xattr.user.overlay.opaque": "y", "other": "x"}},
		{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0o600, ModTime: mt},
		{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0o644, ModTime: mt}, // replaced
		{Name: "hl", Typeflag: tar.TypeLink, Linkname: "f", ModTime: mt},
		{Name: "hl", Typeflag: tar.TypeLink, Linkname: "f", ModTime: mt}, // replaced
		{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: mt},
		{Name: "x", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: mt}, // file -> dir
		{Name: "y", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: mt},
		{Name: "y/z", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: mt},  // parent is a file
		{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o644, ModTime: mt}, // skipped
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: mt},   // the root itself
	}
	for _, h := range hdrs {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte("a")) //nolint:errcheck
		}
	}
	tw.Close()
	uid, gid := os.Getuid(), os.Getgid()
	if _, err := Apply(&buf, dest, ApplyOptions{Mode: ModeFlatten, XattrPrefix: "user.", Chown: true,
		IDMap: func(int, int) (int, int) { return uid, gid }}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(dest, "fifo")); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("fifo: %v %v", fi, err)
	}
	a, _ := os.Stat(filepath.Join(dest, "f"))
	b, _ := os.Stat(filepath.Join(dest, "hl"))
	if !os.SameFile(a, b) {
		t.Error("hardlink")
	}
	if fi, _ := os.Stat(filepath.Join(dest, "x")); !fi.IsDir() {
		t.Error("file not replaced by dir")
	}
	if _, err := os.Stat(filepath.Join(dest, "y", "z")); err != nil {
		t.Error("file in the way of a parent dir")
	}
	if _, err := os.Lstat(filepath.Join(dest, "dev")); !os.IsNotExist(err) {
		t.Error("device node created")
	}
	if v, err := getXattr(filepath.Join(dest, "f"), "user.k"); err == nil && string(v) != "v" {
		t.Errorf("xattr %q", v)
	}

	// Rejected or failing entries.
	for name, ents := range map[string][]ent{
		"hardlink to nothing": {{name: "l", typ: tar.TypeLink, link: "/"}},
		"hardlink missing":    {{name: "l", typ: tar.TypeLink, link: "nope"}},
		"bad whiteout":        {{name: ".wh.", typ: tar.TypeReg}},
	} {
		if _, err := Apply(mkTar(t, false, ents...), t.TempDir(), ApplyOptions{Mode: ModeFlatten}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// An xattr the filesystem refuses fails the layer.
	var xb bytes.Buffer
	xw := tar.NewWriter(&xb)
	xw.WriteHeader(&tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, PAXRecords: map[string]string{"SCHILY.xattr.security.capability": "x"}}) //nolint:errcheck
	xw.Close()
	if _, err := Apply(&xb, t.TempDir(), ApplyOptions{Mode: ModeFlatten}); err == nil || !strings.Contains(err.Error(), "xattr") {
		t.Errorf("invalid xattr: %v", err)
	}
	if _, err := Apply(strings.NewReader("\x1f\x8bnot gzip"), t.TempDir(), ApplyOptions{}); err == nil {
		t.Error("broken gzip")
	}
	if _, err := Apply(strings.NewReader("x"), filepath.Join(t.TempDir(), "missing"), ApplyOptions{}); err == nil {
		t.Error("missing destination")
	}
	if _, err := Apply(errReader{}, t.TempDir(), ApplyOptions{}); err == nil {
		t.Error("read error")
	}
	if (ApplyOptions{}).xattrPrefix() != DefaultXattrPrefix {
		t.Error("default xattr prefix")
	}
	// Opaque directory and metadata whiteouts in overlay mode.
	d := t.TempDir()
	if _, err := Apply(mkTar(t, false, ent{name: "o/", typ: tar.TypeDir}, ent{name: "o/.wh..wh..opq", typ: tar.TypeReg},
		ent{name: "o/.wh..wh..plnk", typ: tar.TypeReg}), d, overlayOpts); err != nil {
		t.Fatalf("opaque: %v", err)
	}
	if v, _ := getXattr(filepath.Join(d, "o"), "user.overlay.opaque"); string(v) != "y" {
		t.Errorf("opaque xattr %q", v)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDiffSpecialFiles(t *testing.T) {
	upper := t.TempDir()
	_ = os.WriteFile(filepath.Join(upper, "f"), []byte("data"), 0o644)
	_ = os.Link(filepath.Join(upper, "f"), filepath.Join(upper, "hl"))
	_ = syscall.Mkfifo(filepath.Join(upper, "fifo"), 0o644)
	_ = os.Symlink("f", filepath.Join(upper, "sym"))
	_ = setXattrPlain(filepath.Join(upper, "f"), "user.note", "x")
	var out bytes.Buffer
	if err := Diff(upper, &out, DiffOptions{XattrPrefix: "user.", Epoch: time.Unix(10, 0)}); err != nil {
		t.Fatal(err)
	}
	types := map[string]byte{}
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		types[h.Name] = h.Typeflag
		if h.ModTime.After(time.Unix(10, 0)) {
			t.Errorf("%s: mtime not clamped", h.Name)
		}
	}
	if types["fifo"] != tar.TypeFifo || types["sym"] != tar.TypeSymlink || (types["hl"] != tar.TypeLink && types["f"] != tar.TypeLink) {
		t.Errorf("types %v", types)
	}
	if err := Diff(filepath.Join(upper, "missing"), io.Discard, DiffOptions{}); err == nil {
		t.Error("missing upper")
	}
	if _, err := WriteBlob(upper, filepath.Join(t.TempDir(), "no", "dir", "blob"), DiffOptions{}); err == nil {
		t.Error("blob in a missing dir")
	}
	if !clamp(time.Unix(5, 0), time.Time{}).Equal(time.Unix(5, 0)) {
		t.Error("zero epoch must not clamp")
	}
}
