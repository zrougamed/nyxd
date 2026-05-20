package supervisor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func (s *Supervisor) shouldRegisterDNS(spec ContainerSpec) bool {
	dm := strings.ToLower(strings.TrimSpace(s.dnsMode))
	if dm == "" {
		dm = "auto"
	}
	switch dm {
	case "off", "cni":
		return false
	case "embedded":
		return true
	case "auto":
		if strings.ToLower(strings.TrimSpace(s.netDriver)) == "native" {
			return true
		}
		return spec.EmbedDNS
	default:
		return false
	}
}

func dnsHostForSpec(spec ContainerSpec) string {
	h := strings.TrimSpace(strings.ToLower(spec.Hostname))
	if h != "" {
		return h
	}
	return strings.TrimSpace(strings.ToLower(spec.ID))
}

func (s *Supervisor) registerEmbeddedDNS(spec ContainerSpec, ip string) {
	if !s.shouldRegisterDNS(spec) {
		return
	}
	if strings.TrimSpace(ip) == "" {
		return
	}
	host := dnsHostForSpec(spec)
	if err := s.dns.Register(host, ip); err != nil {
		s.log.Warn("embedded dns register", "host", host, "err", err)
	}
}

func (s *Supervisor) dnsDeregisterForSpec(spec ContainerSpec) {
	if !s.shouldRegisterDNS(spec) {
		return
	}
	host := dnsHostForSpec(spec)
	if err := s.dns.Deregister(host); err != nil {
		s.log.Warn("embedded dns deregister", "host", host, "err", err)
	}
}

func (s *Supervisor) writeBundleResolv(bundleDir string, spec ContainerSpec) (string, error) {
	if !s.shouldRegisterDNS(spec) {
		return "", nil
	}
	gw := net.ParseIP(strings.TrimSpace(s.dnsGateway))
	if gw4 := gw.To4(); gw4 != nil {
		gw = gw4
	} else {
		gw = net.IPv4(10, 88, 0, 1)
	}
	path := filepath.Join(bundleDir, "resolv.conf.host")
	content := fmt.Sprintf("nameserver %s\nsearch nyxd.local.\noptions ndots:0\n", gw.String())
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
