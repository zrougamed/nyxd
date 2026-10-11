//go:build !unix

package daemonlock

import (
	"errors"
	"fmt"
)

// Lock is a no-op placeholder on platforms without flock.
type Lock struct{}

// Acquire always fails on non-Unix builds (nyxd targets Linux).
func Acquire(string) (*Lock, error) {
	return nil, fmt.Errorf("nyxd singleton lock is not supported on this platform")
}

// Close is a no-op.
func (*Lock) Close() error { return nil }

// Holder is not supported on this platform.
func Holder(string) (int, error) { return 0, errors.New("daemon lock not supported on this platform") }

// Signal is not supported on this platform.
func Signal(int) error { return errors.New("not supported on this platform") }
