package supervisor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// unprivilegedPortStart reads net.ipv4.ip_unprivileged_port_start (a var for tests).
var unprivilegedPortStart = func() int {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil {
		return 1024
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 1024
	}
	return n
}

// hostForwards lists the published-port forwards the host backend runs for id.
func (s *Supervisor) hostForwards(id string) []string {
	if f, ok := s.net.(interface{ Forwarded(string) []string }); ok {
		return f.Forwarded(id)
	}
	return nil
}

// restoreHostForwards starts the forwarders of a container re-adopted after a
// daemon restart: they lived in the old nyxd process.
func (s *Supervisor) restoreHostForwards(spec ContainerSpec) {
	if !s.hostNetwork() || len(spec.PortMappings) == 0 || s.net == nil {
		return
	}
	if _, err := s.net.Setup(context.Background(), spec.ID, "", spec.PortMappings, nil); err != nil {
		s.log.Warn("re-adopt: published ports", "id", spec.ID, "err", err)
		return
	}
	if fw := s.hostForwards(spec.ID); len(fw) > 0 {
		s.log.Info("host network: forwarding published ports", "id", spec.ID, "forward", fw)
	}
}

// checkHostPorts fails a rootless start early when the container declares a
// port it cannot bind: on the host network the workload binds its own port
// on the host, and an unprivileged user may not bind ports below
// ip_unprivileged_port_start (CAP_NET_BIND_SERVICE in nyxd's user namespace
// does not count for the host's network). Without this the workload dies
// with "bind: permission denied" buried in its startup output.
func (s *Supervisor) checkHostPorts(spec ContainerSpec) error {
	if !s.rootless || !s.hostNetwork() || len(spec.PortMappings) == 0 {
		return nil
	}
	start := unprivilegedPortStart()
	var low []int
	seen := map[int]bool{}
	for _, pm := range spec.PortMappings {
		// The workload binds the container port; nyxd binds a different host
		// port to forward it. Both are on the host network.
		for _, p := range []int{pm.ContainerPort, pm.HostPort} {
			if p > 0 && p < start && !seen[p] {
				seen[p] = true
				low = append(low, p)
			}
		}
	}
	if len(low) == 0 {
		return nil
	}
	sort.Ints(low)
	ports := make([]string, len(low))
	for i, p := range low {
		ports[i] = strconv.Itoa(p)
	}
	noun := "port"
	if len(low) > 1 {
		noun = "ports"
	}
	return fmt.Errorf("%s %s cannot be used rootless: containers share the host network, and only ports from %d up can be bound without root.\n"+
		"  allow them:  sudo sysctl net.ipv4.ip_unprivileged_port_start=%d   (persist it in /etc/sysctl.d/)\n"+
		"  or make the service listen on a port >= %d",
		noun, strings.Join(ports, ", "), start, low[0], start)
}
