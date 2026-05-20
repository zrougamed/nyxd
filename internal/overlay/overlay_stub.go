//go:build !linux

// Package overlay manages container root filesystems.
// Overlayfs mounts are only implemented on Linux; other platforms return clear errors.
package overlay

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// Manager is a no-op stub on non-Linux builds.
type Manager struct {
	base string
}

// NewManager creates an overlay manager rooted at base.
func NewManager(base string) (*Manager, error) {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, fmt.Errorf("overlay manager init: %w", err)
	}
	return &Manager{base: base}, nil
}

// Prepare is not supported off Linux.
func (m *Manager) Prepare(containerID string, blobPaths []string, cfg *oci.ImageConfig) (string, error) {
	_, _, _ = containerID, blobPaths, cfg
	return "", fmt.Errorf("overlayfs: supported only on linux")
}

// Remove deletes overlay state directories (best-effort without a mount).
func (m *Manager) Remove(containerID string) error {
	cDir := filepath.Join(m.base, containerID)
	return os.RemoveAll(cDir)
}

// MergedDir returns the merged mount path for a container without mounting.
func (m *Manager) MergedDir(containerID string) string {
	return filepath.Join(m.base, containerID, "merged")
}

// IsMounted always returns false on non-Linux.
func (m *Manager) IsMounted(containerID string) bool {
	return false
}
