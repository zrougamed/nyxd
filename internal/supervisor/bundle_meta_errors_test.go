package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBundleMetaErrors(t *testing.T) {
	meta := bundleRunMeta{Image: "app:1"}
	dir := t.TempDir()
	// The metadata file's place is taken by a directory.
	os.MkdirAll(filepath.Join(dir, bundleMetaFileName, "x"), 0o700) //nolint:errcheck
	if err := writeBundleRunMeta(dir, meta); err == nil {
		t.Error("rename over a directory")
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600) //nolint:errcheck
	if err := writeBundleRunMeta(filepath.Join(file, "b"), meta); err == nil {
		t.Error("bundle dir under a file")
	}
	if os.Getuid() != 0 {
		ro := t.TempDir()
		os.Chmod(ro, 0o500)                       //nolint:errcheck
		t.Cleanup(func() { os.Chmod(ro, 0o700) }) //nolint:errcheck
		if err := writeBundleRunMeta(ro, meta); err == nil {
			t.Error("write into a read-only bundle dir")
		}
	}
}
