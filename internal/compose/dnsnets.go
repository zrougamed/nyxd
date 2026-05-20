package compose

// ServiceUsesOnlyInternalNetworks reports whether every network this service is
// attached to is declared under stack.networks with internal: true.
//
// Implicit default network: when the service omits networks:, Compose attaches
// to "default" if that key exists; otherwise if the stack declares exactly one
// network, that network's internal flag is used. If there are no top-level
// networks, returns false.
func ServiceUsesOnlyInternalNetworks(stack *Stack, svc Service) bool {
	if len(stack.Networks) == 0 {
		return false
	}
	nets := svc.Networks
	if len(nets) == 0 {
		if nw, ok := stack.Networks["default"]; ok {
			return nw.Internal
		}
		if len(stack.Networks) == 1 {
			for _, nw := range stack.Networks {
				return nw.Internal
			}
		}
		return false
	}
	for _, name := range nets {
		nw, ok := stack.Networks[name]
		if !ok || !nw.Internal {
			return false
		}
	}
	return true
}
