package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
)

// HostBackend shares the host network with containers: no network namespace,
// no bridge and no NAT. It needs no privileges and no extra binaries, which
// makes it the default for rootless nyxd.
//
// A container binds its ports on the host itself, so a published port whose
// host and container numbers match needs nothing. When they differ
// ("8443:1443"), nyxd forwards the host port to the container port on the
// loopback, like Docker's userland proxy, so Compose ports: work rootless.
type HostBackend struct {
	mu       sync.Mutex
	forwards map[string][]*forwarder // by container ID
}

// NewHostBackend returns a HostBackend.
func NewHostBackend() *HostBackend { return &HostBackend{} }

// EnsureNetwork is a no-op.
func (*HostBackend) EnsureNetwork() error { return nil }

// Setup starts forwarders for the published ports whose host and container
// numbers differ; containers use the host's addresses, so the IP is empty.
func (h *HostBackend) Setup(_ context.Context, id, _ string, ports []PortMapping, _ *SetupOptions) (string, error) {
	h.stop(id) // a restart sets up the same container again
	var started []*forwarder
	for _, p := range ports {
		if p.HostPort <= 0 || p.HostPort == p.ContainerPort {
			continue
		}
		f, err := startForwarder(p)
		if err != nil {
			for _, s := range started {
				s.close()
			}
			return "", forwardError(p, err)
		}
		started = append(started, f)
	}
	if len(started) > 0 {
		h.mu.Lock()
		if h.forwards == nil {
			h.forwards = map[string][]*forwarder{}
		}
		h.forwards[id] = started
		h.mu.Unlock()
	}
	return "", nil
}

// Teardown stops the container's forwarders.
func (h *HostBackend) Teardown(_ context.Context, id, _ string) error {
	h.stop(id)
	return nil
}

// Forwarded lists the active forwards of a container as "host->container/proto".
func (h *HostBackend) Forwarded(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, f := range h.forwards[id] {
		out = append(out, fmt.Sprintf("%d->%d/%s", f.pm.HostPort, f.pm.ContainerPort, f.proto()))
	}
	return out
}

func (h *HostBackend) stop(id string) {
	h.mu.Lock()
	fs := h.forwards[id]
	delete(h.forwards, id)
	h.mu.Unlock()
	for _, f := range fs {
		f.close()
	}
}

func forwardError(p PortMapping, err error) error {
	proto := strings.ToLower(p.Protocol)
	if proto == "" {
		proto = "tcp"
	}
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return fmt.Errorf("publish %d:%d/%s: host port %d is already in use", p.HostPort, p.ContainerPort, proto, p.HostPort)
	case errors.Is(err, syscall.EACCES):
		return fmt.Errorf("publish %d:%d/%s: host port %d needs root (see net.ipv4.ip_unprivileged_port_start)", p.HostPort, p.ContainerPort, proto, p.HostPort)
	default:
		return fmt.Errorf("publish %d:%d/%s: %w", p.HostPort, p.ContainerPort, proto, err)
	}
}
