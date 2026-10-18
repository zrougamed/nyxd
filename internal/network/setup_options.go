package network

// SetupOptions configures optional per-container behavior for [Backend.Setup].
// A nil pointer means defaults (all fields false / empty).
type SetupOptions struct {
	// Internal blocks IPv4 egress from the container to destinations outside the
	// bridge CIDR (compose: networks.*.internal: true). Implemented for the native
	// driver via nftables forward rules; ignored by the CNI driver until wired.
	Internal bool
	// Bridge is the host bridge interface to attach the container veth to.
	// Empty uses the daemon default (nyxbr0 for native).
	Bridge string
	// Subnet is the IPv4 CIDR for IPAM on Bridge (required when Bridge is set).
	Subnet string
	// Gateway is the IPv4 address of Bridge (required when Bridge is set).
	Gateway string
}
