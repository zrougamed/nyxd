package netdns

import (
	"log/slog"
	"net"
	"strings"
)

// Backend registers short-lived names for containers (embedded resolver) or is a no-op.
type Backend interface {
	Register(host, ip string) error
	Deregister(host string) error
	Shutdown()
}

// NewBackend selects the DNS implementation for nyxd.
// dnsMode: auto (default), embedded, cni, off.
// netDriver is reserved for future policy; per-container rules live in the supervisor.
func NewBackend(dnsMode, netDriver string, log *slog.Logger, gw net.IP) Backend {
	_ = netDriver
	m := strings.ToLower(strings.TrimSpace(dnsMode))
	if m == "" {
		m = "auto"
	}
	switch m {
	case "off", "cni":
		return Noop{}
	case "embedded", "auto":
		// Zones supports one UDP/53 listener per bridge gateway so compose
		// networks with their own bridges keep isolated name spaces.
		return NewZones(log, gw)
	default:
		return Noop{}
	}
}
