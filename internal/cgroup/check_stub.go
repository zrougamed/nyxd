//go:build !linux

package cgroup

// CheckUnifiedV2 is a no-op on non-Linux build targets (e.g. darwin dev builds).
func CheckUnifiedV2() error { return nil }
