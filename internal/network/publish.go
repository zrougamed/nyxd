package network

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDockerPublish parses a single Docker-style --publish / -p value.
// Supported forms:
//   - host:container (e.g. 8080:80)
//   - ip:host:container (e.g. 127.0.0.1:8080:80) — host IP is accepted but may be ignored by the network backend
//   - port (e.g. 80) — same host and container TCP port
//   - …/tcp or …/udp suffix
func ParseDockerPublish(s string) (PortMapping, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return PortMapping{}, fmt.Errorf("empty publish spec")
	}
	proto := "tcp"
	if i := strings.LastIndex(s, "/"); i >= 0 {
		p := strings.ToLower(strings.TrimSpace(s[i+1:]))
		s = strings.TrimSpace(s[:i])
		switch p {
		case "tcp", "udp":
			proto = p
		default:
			return PortMapping{}, fmt.Errorf("invalid protocol %q in %q", p, s)
		}
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		p, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || p < 1 || p > 65535 {
			return PortMapping{}, fmt.Errorf("invalid port %q", parts[0])
		}
		return PortMapping{HostPort: p, ContainerPort: p, Protocol: proto}, nil
	case 2:
		hp, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		cp, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || hp < 1 || hp > 65535 || cp < 1 || cp > 65535 {
			return PortMapping{}, fmt.Errorf("invalid publish %q", s)
		}
		return PortMapping{HostPort: hp, ContainerPort: cp, Protocol: proto}, nil
	case 3:
		hp, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
		cp, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err1 != nil || err2 != nil || hp < 1 || hp > 65535 || cp < 1 || cp > 65535 {
			return PortMapping{}, fmt.Errorf("invalid publish %q", s)
		}
		_ = parts[0]
		return PortMapping{HostPort: hp, ContainerPort: cp, Protocol: proto}, nil
	default:
		return PortMapping{}, fmt.Errorf("invalid publish %q", s)
	}
}
