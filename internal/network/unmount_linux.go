//go:build linux

package network

import (
	"errors"

	"golang.org/x/sys/unix"
)

func detachUnmount(path string) error {
	err := unix.Unmount(path, unix.MNT_DETACH)
	if err != nil {
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	return nil
}
