package network

import "context"

// HostBackend shares the host network with containers: no network namespace,
// no bridge, no NAT and no published-port rules. It needs no privileges and
// no extra binaries, which makes it the default for rootless nyxd.
type HostBackend struct{}

// EnsureNetwork is a no-op.
func (HostBackend) EnsureNetwork() error { return nil }

// Setup is a no-op; containers use the host's addresses.
func (HostBackend) Setup(context.Context, string, string, []PortMapping, *SetupOptions) (string, error) {
	return "", nil
}

// Teardown is a no-op.
func (HostBackend) Teardown(context.Context, string, string) error { return nil }
