//go:build linux

package cgroup

import (
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"golang.org/x/sys/unix"
)

func TestCheckUnifiedV2(t *testing.T) {
	// The host may or may not use cgroup v2 only; both answers are fine.
	_ = CheckUnifiedV2()
}

func TestCheckNotCgroup2(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	if err := unix.Mount("tmpfs", "/sys/fs/cgroup", "tmpfs", 0, ""); err != nil {
		t.Skip(err)
	}
	if err := CheckUnifiedV2(); err == nil {
		t.Error("tmpfs accepted as cgroup2")
	}
	if err := unix.Unmount("/sys/fs/cgroup", unix.MNT_DETACH); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", "/sys/fs", "tmpfs", 0, ""); err != nil {
		t.Skip(err)
	}
	if err := CheckUnifiedV2(); err == nil {
		t.Error("missing /sys/fs/cgroup accepted")
	}
}
