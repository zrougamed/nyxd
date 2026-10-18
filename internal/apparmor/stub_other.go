//go:build !linux

package apparmor

import "testing"

// StubForTest is a no-op off Linux (Available is always false).
func StubForTest(t testing.TB, available bool, profiles string) (restore func()) {
	t.Helper()
	return func() {}
}
