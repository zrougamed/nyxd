//go:build linux

package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"golang.org/x/sys/unix"
)

func TestNetNSLifecycle(t *testing.T) {
	if !nsrun.Run(t, "/run") {
		return
	}
	p, err := CreateNetNS("c1")
	if err != nil {
		t.Fatal(err)
	}
	// A leftover mount at the path is replaced.
	if p2, err := CreateNetNS("c1"); err != nil || p2 != p {
		t.Fatalf("recreate: %v", err)
	}
	if err := DeleteNetNS("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("netns path left behind")
	}
	if err := DeleteNetNS("c1"); err != nil {
		t.Errorf("delete twice: %v", err)
	}
	if err := detachUnmount("/nonexistent"); err != nil {
		t.Errorf("unmount of a missing path: %v", err)
	}
	// unshare missing from PATH.
	t.Setenv("PATH", "/nonexistent")
	if _, err := CreateNetNS("c2"); err == nil {
		t.Error("netns without unshare")
	}
	t.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	// A non-empty directory where the path should be.
	dir := filepath.Join("/run/nyxd/netns", "c3")
	_ = os.MkdirAll(filepath.Join(dir, "x"), 0o700)
	if _, err := CreateNetNS("c3"); err == nil {
		t.Error("netns over a non-empty dir")
	}
	// /run/nyxd not writable.
	if err := unix.Mount("tmpfs", "/run", "tmpfs", unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNetNS("c4"); err == nil {
		t.Error("netns on a read-only /run")
	}
}
