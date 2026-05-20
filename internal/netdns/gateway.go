package netdns

import (
	"net"
	"os"
	"strings"
)

// DefaultGateway returns the IPv4 bridge gateway used for native networking
// and for the embedded resolver bind address. Matches NYXD_GATEWAY_IP used by
// internal/network/native when unset.
func DefaultGateway() net.IP {
	s := strings.TrimSpace(os.Getenv("NYXD_GATEWAY_IP"))
	if s == "" {
		s = "10.88.0.1"
	}
	ip := net.ParseIP(s)
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return net.IPv4(10, 88, 0, 1)
}
