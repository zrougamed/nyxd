package compose

import "strings"

// ServiceUsesHostNetwork reports whether the service shares the host network
// namespace: network_mode: host, or attachment to a compose network with
// driver: host.
func ServiceUsesHostNetwork(stack *Stack, svc Service) bool {
	mode := strings.ToLower(strings.TrimSpace(svc.NetworkMode))
	if mode == "host" {
		return true
	}
	if stack == nil || len(svc.Networks) == 0 {
		return false
	}
	for _, name := range svc.Networks {
		nw, ok := stack.Networks[name]
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(nw.Driver), "host") {
			return true
		}
	}
	return false
}

// StackUsesHostNetwork is true when any service opts into the host network.
func StackUsesHostNetwork(st *Stack) bool {
	if st == nil {
		return false
	}
	for _, svc := range st.Services {
		if ServiceUsesHostNetwork(st, svc) {
			return true
		}
	}
	return false
}
