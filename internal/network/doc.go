// Package network wires host and per-container networking for nyxd.
//
// # Backend
//
// [Backend] is the interface used by the supervisor: EnsureNetwork, Setup,
// and Teardown. Implementations:
//
//   - [Manager] — execs standard CNI plugins from -cni-bin-dir (use nyxd -net-driver=cni).
//   - Package native — in-process bridge, veth, and IPAM (default -net-driver=native).
//
// [PortMapping] is defined here and reused by the native implementation so
// supervisor port maps and the native stack share one type.
//
// Operational guide: docs/networking.md
package network
