package network

// SetupOptions configures optional per-container behavior for [Backend.Setup].
// A nil pointer means defaults (all fields false).
type SetupOptions struct {
	// Internal blocks IPv4 egress from the container to destinations outside the
	// bridge CIDR (compose: networks.*.internal: true). Implemented for the native
	// driver via nftables forward rules; ignored by the CNI driver until wired.
	Internal bool
}
