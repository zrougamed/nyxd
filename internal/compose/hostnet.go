package compose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zrougamed/nyxd/internal/network"
)

// HostNetworkWarnings lists what a stack asks for that the host network
// (rootless nyxd, daemon -net-driver=host, or per-service network_mode: host)
// cannot give it, so compose up can say so instead of silently doing something else.
//
// When the stack mixes host and bridge services, only host-network services
// are considered for port-collision warnings. Internal-network isolation
// warnings still cover every service that declares an internal network while
// the daemon (or any service) is on the host network.
func HostNetworkWarnings(st *Stack) []string {
	var out []string
	names := make([]string, 0, len(st.Services))
	for n := range st.Services {
		names = append(names, n)
	}
	sort.Strings(names)

	// internal: networks are not isolated.
	var internalSvcs []string
	for _, n := range names {
		svc := st.Services[n]
		if ServiceUsesHostNetwork(st, svc) {
			// network_mode host ignores Networks; no isolation claim to warn about.
			continue
		}
		for _, net := range svc.Networks {
			if st.Networks[net].Internal {
				internalSvcs = append(internalSvcs, n)
				break
			}
		}
	}
	if len(internalSvcs) > 0 {
		out = append(out, fmt.Sprintf("internal networks are not isolated on the host network: %s can be reached from outside on the ports they listen on (bind them to 127.0.0.1, or run nyxd as root for isolated networks)",
			strings.Join(internalSvcs, ", ")))
	}

	// Two services that listen on the same port collide: they share one network.
	// Per-service network_mode host rejects ports at spec build; collisions here
	// matter when the whole daemon uses the host driver (published ports still set).
	owner := map[string]string{}
	for _, n := range names {
		svc := st.Services[n]
		if StackUsesHostNetwork(st) && !ServiceUsesHostNetwork(st, svc) {
			// Bridge service in a mixed stack: its published ports are DNAT'd,
			// not a shared bind on the host namespace.
			continue
		}
		for _, p := range svc.Ports {
			pm, err := network.ParseDockerPublish(p)
			if err != nil {
				continue
			}
			proto := strings.ToLower(pm.Protocol)
			if proto == "" {
				proto = "tcp"
			}
			for _, port := range []int{pm.ContainerPort, pm.HostPort} {
				key := fmt.Sprintf("%d/%s", port, proto)
				if prev, ok := owner[key]; ok && prev != n {
					out = append(out, fmt.Sprintf("services %s and %s both use port %s; on the host network only one can bind it", prev, n, key))
				} else {
					owner[key] = n
				}
			}
		}
	}
	return out
}
