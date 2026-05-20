package network

import "context"

// Backend configures host-wide networking (EnsureNetwork) and per-container
// setup/teardown. Implemented by the exec-based *Manager (CNI plugins) and
// by internal/network/native.Manager (in-process, no /opt/cni/bin).
//
// See docs/networking.md for daemon flags and how the supervisor uses this interface.
type Backend interface {
	EnsureNetwork() error
	Setup(ctx context.Context, containerID, netNSPath string, ports []PortMapping, opts *SetupOptions) (string, error)
	Teardown(ctx context.Context, containerID, netNSPath string) error
}
