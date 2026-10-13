package compose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zrougamed/nyxd/internal/network"
)

// HostNetworkWarnings lists what a stack asks for that the host network
// (rootless nyxd) cannot give it, so compose up can say so instead of
// silently doing something else.
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
	owner := map[string]string{}
	for _, n := range names {
		for _, p := range st.Services[n].Ports {
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
