//go:build !linux

package network

func detachUnmount(path string) error {
	return nil
}
