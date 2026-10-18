//go:build linux

// Package apparmor reports whether the host can apply OCI AppArmor profiles.
package apparmor

import (
	"bufio"
	"os"
	"strings"
)

// availableCheck is the host probe (overridable in tests).
var availableCheck = func() bool {
	_, err := os.Stat("/sys/kernel/security/apparmor")
	return err == nil
}

// Available reports whether the kernel AppArmor interface is mounted.
// Distros without AppArmor (typical Fedora/RHEL with SELinux, Alpine, etc.)
// return false so compose apparmor_profile can be ignored instead of failing crun.
func Available() bool {
	return availableCheck()
}

// profilesPath is the kernel profile list (overridable in tests).
var profilesPath = "/sys/kernel/security/apparmor/profiles"

// ProfileLoaded reports whether name is present in the kernel's loaded profile list.
// When AppArmor is unavailable, returns false.
func ProfileLoaded(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || !Available() {
		return false
	}
	return profileInFile(profilesPath, name)
}

func profileInFile(path, name string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// Lines look like: profile-name (enforce) or profile-name (complain)
		line := sc.Text()
		if i := strings.IndexByte(line, ' '); i > 0 {
			line = line[:i]
		}
		if line == name {
			return true
		}
	}
	return false
}
