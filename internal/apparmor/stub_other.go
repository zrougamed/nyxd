//go:build !linux

package apparmor

import "testing"

// StubForTest is a no-op on non-Linux GOOS (Available is always false there).
func StubForTest(t testing.TB, available bool, profiles string) (restore func()) {
	t.Helper()
	return func() {}
}
