//go:build unix

package daemonlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock is an exclusive OS advisory lock held for the lifetime of one nyxd process.
type Lock struct {
	path string
	f    *os.File
}

// Acquire takes a non-blocking exclusive flock on {baseDir}/run/nyxd-daemon.lock.
// Call Close when the daemon exits so another instance can start.
func Acquire(baseDir string) (*Lock, error) {
	path := filepath.Join(baseDir, "run", "nyxd-daemon.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o711); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("another nyxd is already running (lock %s)", path)
		}
		return nil, fmt.Errorf("daemon lock %s: %w", path, err)
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	return &Lock{path: path, f: f}, nil
}

// Close releases the flock and closes the lock file.
func (l *Lock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
