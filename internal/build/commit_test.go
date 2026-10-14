//go:build linux

package build

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/overlay"
	"github.com/zrougamed/nyxd/pkg/oci"
)

func newCommitBuilder(t *testing.T) (*Builder, *image.Store, string) {
	t.Helper()
	base := t.TempDir()
	store, err := image.NewStore(filepath.Join(base, "images"))
	if err != nil {
		t.Fatal(err)
	}
	ovl, err := overlay.NewManager(filepath.Join(base, "overlay"), overlay.Options{Rootless: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{Store: store, Overlay: ovl, BaseDir: base}) // no Log: slog.Default
	if err != nil {
		t.Fatal(err)
	}
	return b, store, base
}

func TestCommitLayerAndImage(t *testing.T) {
	b, store, base := newCommitBuilder(t)
	if _, _, err := b.commitLayer("none"); err == nil {
		t.Error("commit of a missing upper dir")
	}
	upper := b.cfg.Overlay.UpperDir("c1")
	os.MkdirAll(upper, 0o755) //nolint:errcheck
	if d, _, err := b.commitLayer("c1"); err != nil || d != nil {
		t.Errorf("empty upper: %v %v", d, err)
	}
	os.WriteFile(filepath.Join(upper, "f"), []byte("x"), 0o644) //nolint:errcheck
	desc, diffID, err := b.commitLayer("c1")
	if err != nil || desc == nil || diffID == "" {
		t.Fatalf("commit: %v %v", desc, err)
	}

	// Defaults for an image without OS/architecture, docker media types
	// rewritten, and a generated tag.
	bd := &build{b: b, out: io.Discard}
	st := &stageState{layers: []oci.Descriptor{{MediaType: oci.MediaTypeDockerLayerGzip, Digest: desc.Digest, Size: desc.Size}, {Digest: desc.Digest, Size: desc.Size}}}
	res, err := bd.commitImage(st)
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := store.ListImageRefs()
	if len(refs) != 1 || !strings.HasPrefix(refs[0], "nyx-build:") {
		t.Fatalf("refs %v (result %+v)", refs, res)
	}
	m, cfg, err := store.LoadImageMeta(refs[0])
	if err != nil || cfg.OS != "linux" || cfg.Architecture == "" || m.Layers[0].MediaType != oci.MediaTypeImageLayerGzip || m.Layers[1].MediaType != oci.MediaTypeImageLayerGzip {
		t.Errorf("image %+v %+v %v", m, cfg, err)
	}

	if os.Getuid() == 0 {
		return
	}
	// The store cannot take the image or the config.
	imagesDir := filepath.Join(base, "images", "images")
	os.MkdirAll(imagesDir, 0o755) //nolint:errcheck
	os.Chmod(imagesDir, 0o555)    //nolint:errcheck
	bd.opts.Tags = []string{"x/y:1"}
	if _, err := bd.commitImage(&stageState{}); err == nil || !strings.Contains(err.Error(), "tag x/y:1") {
		t.Errorf("tag into a read-only store: %v", err)
	}
	os.Chmod(imagesDir, 0o755) //nolint:errcheck
	blobs := filepath.Join(base, "images", "blobs", "sha256")
	os.Chmod(blobs, 0o555)                       //nolint:errcheck
	t.Cleanup(func() { os.Chmod(blobs, 0o755) }) //nolint:errcheck
	if _, err := bd.commitImage(&stageState{cfg: oci.ImageConfig{OS: "linux", Architecture: "arm64"}}); err == nil {
		t.Error("config into a read-only store")
	}
	os.WriteFile(filepath.Join(upper, "g"), []byte("y"), 0o644) //nolint:errcheck
	if _, _, err := b.commitLayer("c1"); err == nil {
		t.Error("layer into a read-only store")
	}
	os.Chmod(blobs, 0o755)                 //nolint:errcheck
	os.Chmod(filepath.Join(upper, "g"), 0) //nolint:errcheck
	if _, _, err := b.commitLayer("c1"); err == nil || !strings.Contains(err.Error(), "commit layer") {
		t.Errorf("unreadable file in the upper dir: %v", err)
	}
}
