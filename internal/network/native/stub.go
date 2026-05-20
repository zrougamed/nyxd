//go:build !linux

// Package native implements in-process container networking on Linux only.
package native

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zrougamed/nyxd/internal/network"
)

// Manager is a stub on non-Linux platforms.
type Manager struct {
	log *slog.Logger
	mu  sync.Mutex
}

// NewManager returns a stub manager.
func NewManager(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// EnsureNetwork reports that native networking is unavailable.
func (m *Manager) EnsureNetwork() error {
	return fmt.Errorf("native network: supported only on linux")
}

// Setup is unsupported off Linux.
func (m *Manager) Setup(ctx context.Context, containerID, netNSPath string, ports []network.PortMapping, opts *network.SetupOptions) (string, error) {
	_, _, _, _, _ = ctx, containerID, netNSPath, ports, opts
	return "", fmt.Errorf("native network: supported only on linux")
}

// Teardown is a no-op on non-Linux.
func (m *Manager) Teardown(ctx context.Context, containerID, _ string) error {
	_, _ = ctx, containerID
	return nil
}

// IP returns empty on non-Linux.
func (m *Manager) IP(containerID string) string {
	return ""
}

// Allocations returns an empty map on non-Linux.
func (m *Manager) Allocations() map[string]string {
	return map[string]string{}
}

var _ network.Backend = (*Manager)(nil)
