package image

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func storeTestImage(t *testing.T, s *Store, ref string) {
	t.Helper()
	layer, err := s.PutBlob([]byte("layer " + ref))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: "amd64"}
	cd, cdata, err := ConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutBlob(cdata); err != nil {
		t.Fatal(err)
	}
	m := &oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{Digest: cd}, Layers: []oci.Descriptor{{Digest: layer}}}
	if err := s.SaveImage(ref, m, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestListAndPruneByReference(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"alpine:3.20", "localhost:5000/team/app:1", "ghcr.io/o/tool:2"} {
		storeTestImage(t, s, ref)
	}
	// An image stored before references were recorded: listed by directory.
	legacy := filepath.Join(s.root, "images", "library_busybox", "1")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "manifest.json"), []byte(`{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	refs, err := s.ListImageRefs()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(refs, " "); got != "alpine:3.20 ghcr.io/o/tool:2 library_busybox:1 localhost:5000/team/app:1" {
		t.Fatalf("refs: %s", got)
	}

	// In use under another spelling of the same reference: kept.
	keep := map[string]struct{}{"docker.io/library/alpine:3.20": {}, "busybox:1": {}}
	dry, err := s.PruneImagesNotIn(keep, true)
	if err != nil || strings.Join(dry, " ") != "ghcr.io/o/tool:2 localhost:5000/team/app:1" {
		t.Fatalf("dry run: %v %v", dry, err)
	}
	removed, err := s.PruneImagesNotIn(keep, false)
	if err != nil || len(removed) != 2 {
		t.Fatalf("prune: %v %v", removed, err)
	}
	if _, _, err := s.LoadImageMeta("alpine:3.20"); err != nil {
		t.Errorf("in-use image pruned: %v", err)
	}
	if _, _, err := s.LoadImageMeta("localhost:5000/team/app:1"); err == nil {
		t.Error("unused image kept")
	}
	if s.HasBlob(sha256Digest([]byte("layer ghcr.io/o/tool:2"))) {
		t.Error("blob of a pruned image kept")
	}
	if !s.HasBlob(sha256Digest([]byte("layer alpine:3.20"))) {
		t.Error("blob of a kept image removed")
	}
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
