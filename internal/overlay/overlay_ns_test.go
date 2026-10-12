//go:build linux

package overlay

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// blob writes a one-file layer under dir, named by its sha256 like the store does.
func blob(t *testing.T, dir, body string) string {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	tw.WriteHeader(&tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}) //nolint:errcheck
	tw.Write([]byte(body))                                                                             //nolint:errcheck
	tw.Close()
	sum := sha256.Sum256(b.Bytes())
	p := filepath.Join(dir, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOverlayLifecycle(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	base := t.TempDir()
	// A legacy cache left by an old nyxd is removed on start.
	_ = os.MkdirAll(filepath.Join(base, legacyCacheDir, "x"), 0o755)
	m, err := NewManager(base, Options{Rootless: true, SingleID: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, legacyCacheDir)); !os.IsNotExist(err) {
		t.Error("legacy cache kept")
	}
	blobs := t.TempDir()
	b1, b2 := blob(t, blobs, "one"), blob(t, blobs, "two")
	merged, err := m.Prepare("c1", []string{b1, b2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(merged, "f")); string(got) != "two" {
		t.Errorf("upper layer must win: %q", got)
	}
	if !m.IsMounted("c1") || m.MergedDir("c1") != merged || m.XattrPrefix() != "user." {
		t.Error("mounted state")
	}
	// Layers are shared between containers; an image without layers works.
	if _, err := m.Prepare("c2", []string{b1}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare("c3", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if err := m.Remove(id); err != nil {
			t.Fatal(err)
		}
	}
	if m.IsMounted("c1") {
		t.Error("still mounted after remove")
	}
	if err := m.Unmount("never"); err != nil {
		t.Errorf("unmount of an unmounted container: %v", err)
	}
	// Failures: bad digest name, corrupt layer, missing blob, rootful mount options.
	bad := filepath.Join(blobs, "x..y")
	_ = os.WriteFile(bad, []byte("x"), 0o644)
	if _, err := m.Prepare("e1", []string{bad}, nil); err == nil {
		t.Error("invalid digest name")
	}
	corrupt := filepath.Join(blobs, hex.EncodeToString(make([]byte, 32)))
	_ = os.WriteFile(corrupt, []byte("not a tar"), 0o644)
	if _, err := m.Prepare("e2", []string{corrupt}, nil); err == nil {
		t.Error("corrupt layer")
	}
	if _, err := m.Prepare("e3", []string{filepath.Join(blobs, hex.EncodeToString(bytes.Repeat([]byte{1}, 32)))}, nil); err == nil {
		t.Error("missing blob")
	}
	rootful, _ := NewManager(t.TempDir(), Options{})
	if rootful.XattrPrefix() == "user." {
		t.Error("rootful xattr prefix")
	}
	// Rootful options (no userxattr): newer kernels accept them here too.
	if _, err := rootful.Prepare("r", []string{b1}, nil); err == nil {
		_ = rootful.Remove("r")
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	if _, err := NewManager(filepath.Join(f, "x"), Options{}); err == nil {
		t.Error("base under a file")
	}
	mf, _ := NewManager(t.TempDir(), Options{Rootless: true})
	_ = os.WriteFile(filepath.Join(mf.base, "c"), nil, 0o600)
	if _, err := mf.Prepare("c", nil, nil); err == nil {
		t.Error("container dir is a file")
	}
}
