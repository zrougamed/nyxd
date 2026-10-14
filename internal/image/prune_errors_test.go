package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest stored without mediaType is pushed as an OCI manifest.
func TestPushBareManifest(t *testing.T) {
	s := newStore(t)
	seedImage(t, s, "src/app:1")
	m, cfg, err := s.LoadImageMeta("src/app:1")
	if err != nil {
		t.Fatal(err)
	}
	m.MediaType = ""
	r := newScripted(t)
	if err := s.SaveImage(r.host()+"/bare:1", m, cfg); err != nil {
		t.Fatal(err)
	}
	var mt string
	if _, err := s.Push(context.Background(), r.host()+"/bare:1", nil, func(ev PullEvent) {
		if ev.Phase == "manifest" {
			mt = ev.MediaType
		}
	}); err != nil || !strings.Contains(mt, "oci.image.manifest") {
		t.Errorf("push: %v, media type %q", err, mt)
	}
}

func TestPruneAndRemoveErrors(t *testing.T) {
	s := newStore(t)
	seedImage(t, s, "a:1")
	seedImage(t, s, "b:1")
	if _, err := s.ListImageRefs(); err != nil {
		t.Fatal(err)
	}
	blobDir := filepath.Join(s.root, "blobs", "sha256")
	// An unreferenced blob that cannot be deleted.
	stray := filepath.Join(blobDir, strings.Repeat("ab", 32))
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		os.Chmod(blobDir, 0o555) //nolint:errcheck
		if _, err := s.PruneUnreferencedBlobs(); err == nil {
			t.Error("prune in a read-only blob directory")
		}
		if err := s.RemoveImage("a:1"); err == nil || !strings.Contains(err.Error(), "prune blobs") {
			t.Errorf("remove: %v", err)
		}
		if _, err := s.PruneImagesNotIn(map[string]struct{}{}, false); err == nil || !strings.Contains(err.Error(), "prune blobs") {
			t.Errorf("prune images: %v", err)
		}
		os.Chmod(blobDir, 0o755) //nolint:errcheck

	}
	if err := os.RemoveAll(blobDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneUnreferencedBlobs(); err == nil {
		t.Error("prune without a blob directory")
	}
}
