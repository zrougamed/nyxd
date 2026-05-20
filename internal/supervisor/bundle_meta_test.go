package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadBundleMeta(t *testing.T) {
	dir := t.TempDir()
	meta := bundleRunMeta{
		Image:   "alpine:3.19",
		IP:      "10.88.0.5",
		Args:    []string{"/bin/sh"},
		Restart: "unless-stopped",
		Publish: []string{"8080:80/tcp"},
	}
	if err := writeBundleRunMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	got, err := readBundleRunMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != meta.Image || got.IP != meta.IP {
		t.Fatalf("got %+v", got)
	}
	removeBundleMeta(dir)
	if _, err := os.Stat(filepath.Join(dir, bundleMetaFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected meta removed")
	}
}
