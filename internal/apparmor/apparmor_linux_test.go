//go:build linux

package apparmor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileInFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles")
	if err := os.WriteFile(p, []byte("gh-buildkit (enforce)\nunconfined (complain)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !profileInFile(p, "gh-buildkit") || !profileInFile(p, "unconfined") {
		t.Fatal("expected loaded")
	}
	if profileInFile(p, "missing") || profileInFile(filepath.Join(dir, "nope"), "x") {
		t.Fatal("unexpected")
	}
	restore := StubForTest(t, true, "gh-buildkit (enforce)\n")
	defer restore()
	if !ProfileLoaded("gh-buildkit") {
		t.Fatal("ProfileLoaded with test path")
	}
	restore2 := StubForTest(t, false, "gh-buildkit (enforce)\n")
	defer restore2()
	if ProfileLoaded("gh-buildkit") {
		t.Fatal("unavailable host must skip profile list")
	}
}
