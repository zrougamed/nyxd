package netdns

import (
	"net"
	"os"
	"strings"
)

// hostUpstreamResolvers returns UDP DNS addresses (host:53) read from the host's
// resolver configuration, for forwarding queries that are not served locally.
// Order prefers systemd-resolved's real upstream file when present so we avoid
// relying on the stub listener for edge cases.
func hostUpstreamResolvers() []string {
	for _, path := range []string{
		"/run/systemd/resolve/resolv.conf",
		"/etc/resolv.conf",
	} {
		if ns := parseResolvConfNameservers(path); len(ns) > 0 {
			return ns
		}
	}
	return []string{"8.8.8.8:53", "1.1.1.1:53"}
}

func parseResolvConfNameservers(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "nameserver") {
			continue
		}
		host := strings.Trim(fields[1], "[]")
		if ip := net.ParseIP(host); ip != nil {
			out = append(out, net.JoinHostPort(ip.String(), "53"))
		}
	}
	return out
}
