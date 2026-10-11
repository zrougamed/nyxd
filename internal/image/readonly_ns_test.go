//go:build linux

package image

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"golang.org/x/sys/unix"
)

// readOnly bind-mounts dir onto itself read-only: writes fail with EROFS
// even for root of the namespace.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := unix.Mount(dir, dir, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("", dir, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Unmount(dir, unix.MNT_DETACH) })
}

func TestReadOnlyStore(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	s := newStore(t)
	seedImage(t, s, "a/b:1")
	var archive bytes.Buffer
	if err := s.Save(&archive, []string{"a/b:1"}); err != nil {
		t.Fatal(err)
	}
	r := newScripted(t)
	r.publish(t, s, "a/b:1", "1")
	readOnly(t, s.root)
	ctx := context.Background()
	if _, err := s.PutBlob([]byte("new")); err == nil {
		t.Error("PutBlob")
	}
	if _, err := s.TempDir(); err == nil {
		t.Error("TempDir")
	}
	if err := s.AdoptBlob("/nonexistent", "sha256:"+strings.Repeat("c", 64)); err == nil {
		t.Error("AdoptBlob")
	}
	m, cfg, _ := s.LoadImageMeta("a/b:1")
	if err := s.SaveImage("a/b:2", m, cfg); err == nil {
		t.Error("SaveImage")
	}
	if err := s.Tag("a/b:1", "a/b:3"); err == nil {
		t.Error("Tag")
	}
	if err := s.RemoveImage("a/b:1"); err == nil {
		t.Error("RemoveImage")
	}
	if _, err := s.PruneImagesNotIn(nil, false); err == nil {
		t.Error("prune")
	}
	if _, err := s.Load(bytes.NewReader(archive.Bytes())); err == nil {
		t.Error("Load")
	}
	if _, err := s.PullWithProgress(ctx, r.host()+"/app:1", nil, nil); err != nil {
		// Everything is present already: only the metadata write can fail.
		t.Logf("pull into a read-only store: %v", err)
	}
	if _, err := NewStore(filepath.Join(s.root, "sub")); err == nil {
		t.Error("NewStore under a read-only root")
	}
	// A fresh store whose blob dir is read-only: downloads cannot be written.
	s2 := newStore(t)
	readOnly(t, filepath.Join(s2.root, "blobs"))
	if _, err := s2.PullWithProgress(ctx, r.host()+"/app:1", nil, nil); err == nil {
		t.Error("pull into read-only blobs")
	}
	s3 := newStore(t)
	readOnly(t, filepath.Join(s3.root, "images"))
	if _, err := s3.PullWithProgress(ctx, r.host()+"/app:1", nil, nil); err == nil {
		t.Error("pull with read-only image metadata")
	}
}
