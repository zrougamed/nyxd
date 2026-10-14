//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayerDirErrors(t *testing.T) {
	base := t.TempDir()
	m, err := NewManager(base, Options{Rootless: true})
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(t.TempDir(), "blob")
	os.WriteFile(blob, nil, 0o644) //nolint:errcheck // empty tar: an empty layer
	hex := strings.Repeat("a", 64)

	// Something that is not a directory sits where the layer goes.
	os.WriteFile(filepath.Join(base, layersDirName, hex), nil, 0o644) //nolint:errcheck
	if _, err := m.layerDirForDigest("sha256:"+hex, blob); err == nil {
		t.Error("layer dir over a file")
	}
	if _, err := m.layerDirForDigest("sha256:../x", blob); err == nil {
		t.Error("digest with a path")
	}
	if os.Getuid() == 0 {
		return
	}
	layers := filepath.Join(base, layersDirName)
	os.Chmod(layers, 0o500)                       //nolint:errcheck
	t.Cleanup(func() { os.Chmod(layers, 0o700) }) //nolint:errcheck
	if _, err := m.layerDirForDigest("sha256:"+strings.Repeat("b", 64), blob); err == nil {
		t.Error("extract into a read-only layer store")
	}
}

// A legacy cache that cannot be deleted is left in place with a warning.
func TestRemoveLegacyCacheFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	base := t.TempDir()
	sub := filepath.Join(base, legacyCacheDir, "sub")
	os.MkdirAll(sub, 0o755)                           //nolint:errcheck
	os.WriteFile(filepath.Join(sub, "f"), nil, 0o644) //nolint:errcheck
	os.Chmod(sub, 0o500)                              //nolint:errcheck
	t.Cleanup(func() { os.Chmod(sub, 0o755) })        //nolint:errcheck
	removeLegacyCache(base)
	if _, err := os.Stat(sub); err != nil {
		t.Error("undeletable legacy cache vanished")
	}
	os.Chmod(sub, 0o755) //nolint:errcheck
	removeLegacyCache(base)
	if _, err := os.Stat(filepath.Join(base, legacyCacheDir)); !os.IsNotExist(err) {
		t.Error("legacy cache kept")
	}
}
