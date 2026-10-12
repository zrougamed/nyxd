//go:build !windows

package daemonlock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockErrorsAndHolder(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "file"), nil, 0o600)
	if _, err := Acquire(filepath.Join(dir, "file")); err == nil {
		t.Error("lock dir under a file")
	}
	base := t.TempDir()
	_ = os.MkdirAll(filepath.Join(base, "run", "nyxd-daemon.lock"), 0o700)
	if _, err := Acquire(base); err == nil {
		t.Error("lock path is a directory")
	}
	_, _ = Holder(base) // a directory: whatever flock says, no crash
	unreadable := t.TempDir()
	_ = os.MkdirAll(filepath.Join(unreadable, "run"), 0o700)
	_ = os.WriteFile(filepath.Join(unreadable, "run", "nyxd-daemon.lock"), []byte("1\n"), 0o000)
	if os.Getuid() != 0 {
		if _, err := Holder(unreadable); err == nil {
			t.Error("unreadable lock file")
		}
	}
	base = t.TempDir()
	if pid, err := Holder(base); pid != 0 || err != nil {
		t.Errorf("no lock file: %d %v", pid, err)
	}
	l, err := Acquire(base)
	if err != nil {
		t.Fatal(err)
	}
	if pid, err := Holder(base); pid != os.Getpid() || err != nil {
		t.Errorf("holder: %d %v", pid, err)
	}
	if _, err := Acquire(base); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second lock: %v", err)
	}
	// Held but the pid was wiped.
	_ = os.Truncate(filepath.Join(base, "run", "nyxd-daemon.lock"), 0)
	if _, err := Holder(base); err == nil || !strings.Contains(err.Error(), "no pid") {
		t.Errorf("held without pid: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Error("double close")
	}
	var nilLock *Lock
	if nilLock.Close() != nil {
		t.Error("nil close")
	}
	if pid, _ := Holder(base); pid != 0 {
		t.Error("released lock still held")
	}
	if err := Signal(-999999); err == nil {
		t.Error("signal to a bogus pid")
	}
}
