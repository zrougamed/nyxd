//go:build linux

package layer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// nthFailWriter fails its n-th Write and every one after.
type nthFailWriter struct{ n, calls int }

func (w *nthFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.n {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestDiffWriteErrors(t *testing.T) {
	upper := t.TempDir()
	os.MkdirAll(filepath.Join(upper, "d"), 0o755)                                        //nolint:errcheck
	os.WriteFile(filepath.Join(upper, "d", "f"), bytes.Repeat([]byte("x"), 4096), 0o644) //nolint:errcheck
	os.Symlink("d/f", filepath.Join(upper, "s"))                                         //nolint:errcheck
	// Overlay control xattrs are never copied into a layer.
	_ = setXattrPlain(filepath.Join(upper, "d", "f"), "user.overlay.origin", "x")
	_ = setXattrPlain(filepath.Join(upper, "d"), "user.overlay.opaque", "y")

	count := &nthFailWriter{n: 1 << 30}
	if err := Diff(upper, count, DiffOptions{XattrPrefix: "user."}); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= count.calls; n++ {
		if err := Diff(upper, &nthFailWriter{n: n}, DiffOptions{XattrPrefix: "user."}); err == nil {
			t.Errorf("write %d of %d failed, Diff succeeded", n, count.calls)
		}
	}
	var out bytes.Buffer
	if err := Diff(upper, &out, DiffOptions{XattrPrefix: "user."}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("user.overlay.origin")) {
		t.Error("overlay xattr copied into the layer")
	}

	// The blob cannot replace a directory.
	dst := filepath.Join(t.TempDir(), "blob")
	os.MkdirAll(filepath.Join(dst, "x"), 0o755) //nolint:errcheck
	if _, err := WriteBlob(upper, dst, DiffOptions{}); err == nil {
		t.Error("blob over a directory")
	}

	if os.Getuid() == 0 {
		return
	}
	os.Chmod(filepath.Join(upper, "d", "f"), 0) //nolint:errcheck
	if err := Diff(upper, io.Discard, DiffOptions{}); err == nil {
		t.Error("unreadable file")
	}
	os.Chmod(filepath.Join(upper, "d", "f"), 0o644)                  //nolint:errcheck
	os.Chmod(filepath.Join(upper, "d"), 0)                           //nolint:errcheck
	t.Cleanup(func() { os.Chmod(filepath.Join(upper, "d"), 0o755) }) //nolint:errcheck
	if err := Diff(upper, io.Discard, DiffOptions{}); err == nil {
		t.Error("unreadable directory")
	}
}
