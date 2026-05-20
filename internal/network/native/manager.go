//go:build linux

// manager.go — drop-in replacement for the exec-based CNI Manager.
// Implements the same interface as internal/network/cni.go but with
// zero external binaries and zero CNI plugins on disk.
package native

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zrougamed/nyxd/internal/network"
)

// Manager is the native network manager for nyxd.
// It is a drop-in replacement for the CNI exec-based Manager.
// Use NewManager() and call the same Setup/Teardown/EnsureNetwork methods.
type Manager struct {
	log  *slog.Logger
	mu   sync.Mutex
	// tracks containerID → allocated IP for informational purposes
	allocs map[string]string
}

// NewManager creates a native network Manager.
// No CNI conf dir or bin dir needed — everything runs in-process.
func NewManager(log *slog.Logger) *Manager {
	return &Manager{
		log:    log,
		allocs: make(map[string]string),
	}
}

var _ network.Backend = (*Manager)(nil)

// EnsureNetwork creates the nyxbr0 bridge and nftables base rules
// if they don't already exist. Safe to call multiple times.
func (m *Manager) EnsureNetwork() error {
	m.log.Info("ensuring native nyx network", "bridge", BridgeName, "subnet", ContainerSubnet)
	if err := ensureBridge(m.log); err != nil {
		return fmt.Errorf("ensure bridge: %w", err)
	}
	tryEnableRouteLocalnet(m.log)
	tryEnableIPv4Forwarding(m.log)
	ensureNftTable()
	m.log.Info("nyx network ready", "bridge", BridgeName, "gateway", GatewayIP)
	return nil
}

// Setup configures networking for containerID in netNSPath.
// Returns the container's allocated IP address.
func (m *Manager) Setup(ctx context.Context, containerID, netNSPath string, ports []network.PortMapping, opts *network.SetupOptions) (string, error) {
	ip, err := Setup(ctx, containerID, netNSPath, ports, opts, m.log)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.allocs[containerID] = ip
	m.mu.Unlock()
	return ip, nil
}

// Teardown removes all network resources for containerID.
func (m *Manager) Teardown(ctx context.Context, containerID, _ string) error {
	err := Teardown(ctx, containerID, m.log)
	m.mu.Lock()
	delete(m.allocs, containerID)
	m.mu.Unlock()
	return err
}

// IP returns the allocated IP for a running container, or empty string.
func (m *Manager) IP(containerID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allocs[containerID]
}

// Allocations returns a snapshot of all current containerID→IP mappings.
func (m *Manager) Allocations() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.allocs))
	for k, v := range m.allocs {
		out[k] = v
	}
	return out
}
