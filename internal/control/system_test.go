package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemHelpers(t *testing.T) {
	for s, want := range map[string]bool{".1": true, ".12": true, "": false, ".": false, ".x": false, "1": false, ".1a": false} {
		if isRotationSuffix(s) != want {
			t.Errorf("isRotationSuffix(%q)", s)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "merged"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "a", "f"), make([]byte, 10000), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "a", "merged", "big"), make([]byte, 100000), 0o600)
	_ = os.Link(filepath.Join(dir, "a", "f"), filepath.Join(dir, "a", "hardlink"))
	seen := map[[2]uint64]bool{}
	all := diskUsage(dir, nil, map[[2]uint64]bool{})
	skipped := diskUsage(dir, func(p string) bool { return filepath.Base(p) == "merged" }, seen)
	if skipped <= 0 || skipped >= all {
		t.Fatalf("skip: %d vs %d", skipped, all)
	}
	// The hard link is counted once.
	once := diskUsage(filepath.Join(dir, "a", "f"), nil, map[[2]uint64]bool{})
	if again := diskUsage(filepath.Join(dir, "a", "hardlink"), nil, seen); again != 0 || once == 0 {
		t.Fatalf("hard link counted twice: %d (file %d)", again, once)
	}
	if diskUsage(filepath.Join(dir, "missing"), nil, seen) != 0 {
		t.Fatal("missing path")
	}
}
