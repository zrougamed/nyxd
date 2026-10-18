//go:build !linux

package apparmor

// Available is always false on non-Linux GOOS (macOS, Windows, …).
// AppArmor is a Linux LSM; nyx/nyxd still build and run on other platforms
// (CLI, machine/WSL helpers), but compose apparmor_profile cannot apply there.
func Available() bool { return false }

// ProfileLoaded is always false on non-Linux GOOS (see Available).
func ProfileLoaded(string) bool { return false }
