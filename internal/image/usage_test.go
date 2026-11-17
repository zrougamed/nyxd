package image

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// blob writes a blob of n bytes named after hex and returns its digest.
func usageBlob(t *testing.T, s *Store, hex string, n int) string {
	t.Helper()
	d := "sha256:" + strings.Repeat(hex, 64/len(hex))
	if err := os.WriteFile(s.BlobPath(d), make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func usageImage(t *testing.T, s *Store, ref, config string, layers ...string) {
	t.Helper()
	m := oci.Manifest{Config: oci.Descriptor{Digest: config}}
	for _, l := range layers {
		m.Layers = append(m.Layers, oci.Descriptor{Digest: l})
	}
	dir := s.imageDir(ref)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, refFile), []byte(ref), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStoreUsageSharedLayers(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := usageBlob(t, s, "a", 1000) // shared base layer
	cfgA := usageBlob(t, s, "b", 10)
	cfgB := usageBlob(t, s, "c", 20)
	extra := usageBlob(t, s, "d", 300) // only app:1
	orphan := usageBlob(t, s, "e", 7)  // no image: build cache or leftover
	usageImage(t, s, "alpine:3.20", cfgA, base)
	usageImage(t, s, "app:1", cfgB, base, extra)

	u, err := s.Usage(map[string]struct{}{"alpine:3.20": {}})
	if err != nil {
		t.Fatal(err)
	}
	// The shared layer counts once: 1000 + 10 + 20 + 300.
	if u.Size != 1330 || u.Active != 1 || len(u.Images) != 2 {
		t.Fatalf("usage: %+v", u)
	}
	// app:1 is unused; only what alpine does not need is reclaimable.
	if u.Reclaimable != 320 {
		t.Fatalf("reclaimable %d, want 320", u.Reclaimable)
	}
	if a := u.Images[0]; a.Ref != "alpine:3.20" || a.Size != 1010 || a.Unique != 10 || !a.InUse {
		t.Fatalf("alpine: %+v", a)
	}
	if b := u.Images[1]; b.Ref != "app:1" || b.Size != 1320 || b.Unique != 320 || b.InUse {
		t.Fatalf("app: %+v", b)
	}
	if len(u.Unreferenced) != 1 || u.Unreferenced[orphan] != 7 {
		t.Fatalf("unreferenced: %v", u.Unreferenced)
	}

	// The age guard keeps a fresh unreferenced blob (a pull in progress).
	n, freed, err := s.PruneUnreferencedBlobsOlderThan(time.Hour, false)
	if err != nil || n != 0 || freed != 0 {
		t.Fatalf("fresh blob pruned: %d %d %v", n, freed, err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(s.BlobPath(orphan), old, old)
	if n, freed, _ := s.PruneUnreferencedBlobsOlderThan(time.Hour, true); n != 1 || freed != 7 {
		t.Fatalf("dry run: %d %d", n, freed)
	}
	if _, err := os.Stat(s.BlobPath(orphan)); err != nil {
		t.Fatal("dry run deleted the blob")
	}
	if n, _, _ := s.PruneUnreferencedBlobsOlderThan(time.Hour, false); n != 1 {
		t.Fatalf("old blob kept: %d", n)
	}
	if _, err := os.Stat(s.BlobPath(base)); err != nil {
		t.Fatal("a referenced blob was pruned")
	}
	if manifestDigests(filepath.Join(t.TempDir(), "none.json")) != nil {
		t.Fatal("missing manifest")
	}
}
