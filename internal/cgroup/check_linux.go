//go:build linux

package cgroup

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// CheckUnifiedV2 verifies cgroup2 is available and /sys/fs/cgroup is a pure cgroup2
// mount (not hybrid v1+v2).
func CheckUnifiedV2() error {
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return fmt.Errorf("cgroup: read /proc/filesystems: %w", err)
	}
	if !strings.Contains(string(data), "cgroup2") {
		return fmt.Errorf("cgroup v2 not available (kernel too old or cgroup2 not registered)")
	}
	var st unix.Statfs_t
	if err := unix.Statfs("/sys/fs/cgroup", &st); err != nil {
		return fmt.Errorf("cgroup: statfs /sys/fs/cgroup: %w", err)
	}
	if st.Type != unix.CGROUP2_SUPER_MAGIC {
		return fmt.Errorf("/sys/fs/cgroup is not a unified cgroup v2 mount (type %#x; hybrid v1+v2 not supported)", st.Type)
	}
	return nil
}
