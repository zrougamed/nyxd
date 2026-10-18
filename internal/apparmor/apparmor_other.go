//go:build !linux

package apparmor

// Available is always false off Linux (nyxd containers only run on Linux).
func Available() bool { return false }

// ProfileLoaded is always false off Linux.
func ProfileLoaded(string) bool { return false }
