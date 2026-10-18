package supervisor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

func (s *Supervisor) shouldRegisterDNS(spec ContainerSpec) bool {
	if s.hostNetwork() {
		return false
	}
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
	// Caller-supplied dns: servers mean the container does not use the
	// embedded resolver; do not publish the name into any gateway zone.
	if len(spec.DNS) > 0 {
		return
	}
	host := dnsHostForSpec(spec)
	gw := s.resolvGatewayIP(spec)
	if z, ok := s.dns.(netdnsZone); ok {
		if err := z.RegisterOn(gw, host, ip); err != nil {
			s.log.Warn("embedded dns register", "host", host, "gw", gw, "err", err)
		}
		return
	}
	if err := s.dns.Register(host, ip); err != nil {
		s.log.Warn("embedded dns register", "host", host, "err", err)
	}
}

func (s *Supervisor) dnsDeregisterForSpec(spec ContainerSpec) {
	if !s.shouldRegisterDNS(spec) {
		return
	}
	if len(spec.DNS) > 0 {
		return
	}
	host := dnsHostForSpec(spec)
	gw := s.resolvGatewayIP(spec)
	if z, ok := s.dns.(netdnsZone); ok {
		if err := z.DeregisterOn(gw, host); err != nil {
			s.log.Warn("embedded dns deregister", "host", host, "gw", gw, "err", err)
		}
		return
	}
	if err := s.dns.Deregister(host); err != nil {
		s.log.Warn("embedded dns deregister", "host", host, "err", err)
	}
}

// netdnsZone is implemented by multi-gateway DNS backends.
type netdnsZone interface {
	RegisterOn(gateway, host, ip string) error
	DeregisterOn(gateway, host string) error
}

func (s *Supervisor) resolvGatewayIP(spec ContainerSpec) string {
	if gw := strings.TrimSpace(spec.NetworkGateway); gw != "" {
		if ip := net.ParseIP(gw); ip != nil && ip.To4() != nil {
			return ip.To4().String()
		}
	}
	gw := net.ParseIP(strings.TrimSpace(s.dnsGateway))
	if gw4 := gw.To4(); gw4 != nil {
		return gw4.String()
	}
	return "10.88.0.1"
}

func mountHasResolvConf(mounts []bundle.Mount) bool {
	for _, m := range mounts {
		if filepath.Clean(m.Destination) == "/etc/resolv.conf" {
			return true
		}
	}
	return false
}

func (s *Supervisor) writeBundleResolv(bundleDir string, spec ContainerSpec) (string, error) {
	// Compose volume at /etc/resolv.conf is the sole mount for that path.
	if mountHasResolvConf(spec.ExtraMounts) {
		return "", nil
	}
	if s.hostNetwork() {
		// Same network as the host, so the host resolver config applies as is
		// (including loopback resolvers such as systemd-resolved's 127.0.0.53).
		if _, err := os.Stat("/etc/resolv.conf"); err == nil {
			return "/etc/resolv.conf", nil
		}
		return "", nil
	}
	path := filepath.Join(bundleDir, "resolv.conf.host")
	if len(spec.DNS) > 0 {
		var b strings.Builder
		for _, ns := range spec.DNS {
			fmt.Fprintf(&b, "nameserver %s\n", ns)
		}
		b.WriteString("options ndots:0\n")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
	if !s.shouldRegisterDNS(spec) {
		return "", nil
	}
	gw := s.resolvGatewayIP(spec)
	content := fmt.Sprintf("nameserver %s\nsearch nyxd.local.\noptions ndots:0\n", gw)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// writeBundleHosts writes the /etc/hosts of a container on the host network:
// the host's own file (same network, same names) followed by the
// container's hostname and HostAliases on 127.0.0.1. Other drivers keep the
// image's /etc/hosts and resolve names through their own addresses.
func (s *Supervisor) writeBundleHosts(bundleDir string, spec ContainerSpec) (string, error) {
	if !s.hostNetwork() {
		return "", nil
	}
	var names []string
	seen := map[string]bool{}
	for _, n := range append([]string{spec.Hostname}, spec.HostAliases...) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && !seen[n] && !strings.ContainsAny(n, " \t\n#") {
			seen[n] = true
			names = append(names, n)
		}
	}
	var b strings.Builder
	if host, err := os.ReadFile(hostsFile); err == nil {
		b.Write(host)
		if len(host) > 0 && host[len(host)-1] != '\n' {
			b.WriteByte('\n')
		}
	} else {
		b.WriteString("127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost ip6-loopback\n")
	}
	if len(names) > 0 {
		b.WriteString("# nyxd: services sharing the host network\n")
		fmt.Fprintf(&b, "127.0.0.1\t%s\n", strings.Join(names, " "))
	}
	path := filepath.Join(bundleDir, "hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// hostsFile is the host's /etc/hosts (a var for tests).
var hostsFile = "/etc/hosts"
