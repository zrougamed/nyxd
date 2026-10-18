package network

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Identity is the daemon's active L3 attachment for containers (control API).
type Identity struct {
	Driver  string `json:"driver"`
	Bridge  string `json:"bridge,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Subnet  string `json:"subnet,omitempty"`
}

// ActiveIdentity returns the configured network identity for driver.
// For native, Bridge/Gateway/Subnet match the default bridge (nyxbr0) and
// prefer live addresses from the interface when present.
func ActiveIdentity(driver string) Identity {
	d := strings.ToLower(strings.TrimSpace(driver))
	if d == "" {
		d = "native"
	}
	id := Identity{Driver: d}
	switch d {
	case "native":
		id.Bridge = "nyxbr0"
		id.Subnet = envOr("NYXD_CONTAINER_SUBNET", "10.88.0.0/16")
		id.Gateway = envOr("NYXD_GATEWAY_IP", "10.88.0.1")
		if gw, subnet, err := liveBridgeIPv4(id.Bridge); err == nil {
			id.Gateway = gw
			if subnet != "" {
				id.Subnet = subnet
			}
		}
	case "host":
		// No bridge attachment.
	case "cni":
		// CNI owns the data path; report driver only.
	}
	return id
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// liveBridgeIPv4 returns the first IPv4 address and its CIDR on iface.
func liveBridgeIPv4(iface string) (gateway, subnet string, err error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return "", "", err
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return "", "", err
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			continue
		}
		network := ip4.Mask(ipnet.Mask)
		return ip4.String(), fmt.Sprintf("%s/%d", network.String(), ones), nil
	}
	return "", "", fmt.Errorf("no ipv4 on %s", iface)
}
