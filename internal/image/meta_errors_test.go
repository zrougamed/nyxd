package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestLoadImageMetaErrors(t *testing.T) {
	s := newStore(t)
	seedImage(t, s, "app:1")
	dir := s.imageDir("app:1")
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.json"))

	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644) //nolint:errcheck
	if _, _, err := s.LoadImageMeta("app:1"); err == nil {
		t.Error("corrupt config.json")
	}
	os.Remove(filepath.Join(dir, "config.json"))
	if _, _, err := s.LoadImageMeta("app:1"); err == nil {
		t.Error("missing config.json")
	}
	os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o644)           //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("["), 0o644) //nolint:errcheck
	if _, _, err := s.LoadImageMeta("app:1"); err == nil {
		t.Error("corrupt manifest.json")
	}
}

// A registry serving a config blob that is not JSON.
func TestPullBadConfigBlob(t *testing.T) {
	r := newScripted(t)
	cfg := []byte("not a config")
	r.blobs[sha(cfg)] = cfg
	r.manifests["1"] = served{ct: oci.MediaTypeImageManifest, body: jsonOf(t, oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
		Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: sha(cfg), Size: int64(len(cfg))}})}
	if _, err := newStore(t).Pull(context.Background(), r.host()+"/app:1"); err == nil || !strings.Contains(err.Error(), "decode image config") {
		t.Errorf("pull: %v", err)
	}
}
