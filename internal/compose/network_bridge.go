package compose

import (
	"fmt"
	"net"
	"strings"
)

// ServiceBridgeNet is the native bridge attachment for a compose service.
// Empty Bridge means the daemon default (nyxbr0).
type ServiceBridgeNet struct {
	Bridge  string
	Subnet  string
	Gateway string
}

// ResolveServiceBridge picks a custom native bridge when the service lists a
// network that sets both bridge and subnet. Services that omit networks stay
// on the daemon default bridge (nyxbr0). The first matching network in the
// service's list wins.
func ResolveServiceBridge(stack *Stack, svc Service) (ServiceBridgeNet, error) {
	if stack == nil || len(stack.Networks) == 0 || len(svc.Networks) == 0 {
		return ServiceBridgeNet{}, nil
	}
	for _, name := range svc.Networks {
		nw, ok := stack.Networks[name]
		if !ok {
			continue
		}
		bridge := strings.TrimSpace(nw.Bridge)
		subnet := strings.TrimSpace(nw.Subnet)
		if bridge == "" || subnet == "" {
			continue
		}
		gw, err := gatewayFromSubnet(subnet)
		if err != nil {
			return ServiceBridgeNet{}, fmt.Errorf("network %q: %w", name, err)
		}
		if err := validateBridgeName(bridge); err != nil {
			return ServiceBridgeNet{}, fmt.Errorf("network %q: %w", name, err)
		}
		return ServiceBridgeNet{Bridge: bridge, Subnet: subnet, Gateway: gw}, nil
	}
	return ServiceBridgeNet{}, nil
}

func gatewayFromSubnet(cidr string) (string, error) {
	ip, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return "", fmt.Errorf("subnet %q: %w", cidr, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("subnet %q: IPv4 only", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", fmt.Errorf("subnet %q: need IPv4 prefix length <= 30", cidr)
	}
	// First usable host address (network + 1), matching nyxbr0 → 10.88.0.1.
	gw := make(net.IP, len(ip4))
	copy(gw, ip4.Mask(ipnet.Mask))
	gw[len(gw)-1]++
	if !ipnet.Contains(gw) {
		return "", fmt.Errorf("subnet %q: no usable gateway address", cidr)
	}
	return gw.String(), nil
}

func validateBridgeName(name string) error {
	if name == "" {
		return fmt.Errorf("empty bridge name")
	}
	// Linux IFNAMSIZ is 16 including NUL.
	if len(name) > 15 {
		return fmt.Errorf("bridge name %q longer than 15 characters", name)
	}
	// Must be a nyxd-owned name so compose cannot adopt docker0/eth0/etc.
	if name != "nyxbr0" && !strings.HasPrefix(name, "nyxbr") {
		return fmt.Errorf("bridge name %q must start with \"nyxbr\"", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("bridge name %q has invalid character %q", name, r)
	}
	return nil
}
