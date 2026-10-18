//go:build linux

package apparmor

import (
	"os"
	"path/filepath"
	"testing"
)

// StubForTest overrides Available/ProfileLoaded probes for unit tests.
// profiles is the contents of a fake /profiles file (may be empty).
func StubForTest(t testing.TB, available bool, profiles string) (restore func()) {
	t.Helper()
	oldAvail, oldPath := availableCheck, profilesPath
	availableCheck = func() bool { return available }
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles")
	if err := os.WriteFile(p, []byte(profiles), 0o644); err != nil {
		t.Fatal(err)
	}
	profilesPath = p
	return func() {
		availableCheck = oldAvail
		profilesPath = oldPath
	}
}
