//go:build linux

// Package native implements container networking entirely in Go.
// Zero external CNI binaries. Zero external dependencies beyond golang.org/x/sys.
//
// What this replaces:
//   - bridge   CNI plugin  → creates nyxbr0, veth pair, moves peer into netns
//   - host-local CNI plugin → in-process IPAM using a simple file-locked bitmap
//   - loopback CNI plugin  → sets lo up inside the netns
//   - portmap  CNI plugin  → nftables DNAT rules via netlink
//
// All netlink operations use raw syscalls (RTM_* messages) via golang.org/x/sys/unix.
// No netlink library dependency.
package native

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/zrougamed/nyxd/internal/network"

	"golang.org/x/sys/unix"
)

// activeNetConf returns container IPv4 CIDR and gateway from the environment
// (NYXD_CONTAINER_SUBNET, NYXD_GATEWAY_IP) with defaults matching historical nyxd behavior.
func activeNetConf() (cidr, gateway string) {
	cidr = strings.TrimSpace(os.Getenv("NYXD_CONTAINER_SUBNET"))
	if cidr == "" {
		cidr = ContainerSubnet
	}
	gateway = strings.TrimSpace(os.Getenv("NYXD_GATEWAY_IP"))
	if gateway == "" {
		gateway = GatewayIP
	}
	return cidr, gateway
}

// tryEnableRouteLocalnet allows IPv4 DNAT from 127.0.0.1 to a bridge-routed
// container IP (nft output/prerouting) to be forwarded; without it, published
// ports often work from other hosts but not from curl http://localhost:PORT.
func tryEnableRouteLocalnet(log *slog.Logger) {
	for _, p := range []string{
		"/proc/sys/net/ipv4/conf/all/route_localnet",
		"/proc/sys/net/ipv4/conf/default/route_localnet",
	} {
		if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
			log.Warn("route_localnet", "path", p, "err", err)
		}
	}
}

// tryEnableIPv4Forwarding sets net.ipv4.ip_forward so traffic from the bridge
// subnet can be forwarded and SNATed toward the host's default route.
func tryEnableIPv4Forwarding(log *slog.Logger) {
	p := "/proc/sys/net/ipv4/ip_forward"
	if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
		log.Warn("ip_forward", "path", p, "err", err)
	}
}

const (
	// BridgeName is the host-side bridge interface for all nyxd containers.
	BridgeName = "nyxbr0"
	// ContainerSubnet is the IP range allocated to containers.
	ContainerSubnet = "10.88.0.0/16"
	// GatewayIP is nyxbr0's IP — the default gateway for containers.
	GatewayIP = "10.88.0.1"
	// MTU for veth pairs and the bridge.
	MTU = 1500
	// NetNSDir is where bind-mounted network namespaces are stored.
	NetNSDir = "/run/nyxd/netns"
)

// vethInfoPeer is the netlink nested attribute type for the veth peer spec
// (linux/if_link.h: VETH_INFO_PEER).
const vethInfoPeer = 1

// Setup configures networking for a new container:
//  1. Ensures nyxbr0 exists and is up
//  2. Allocates an IP from the subnet
//  3. Creates a veth pair: host-side joins nyxbr0, peer goes into the container netns
//  4. Sets lo up inside the netns
//  5. Adds nftables DNAT rules for any port mappings
//  6. Optionally adds an nft forward drop for compose internal networks (opts.Internal).
//
// Returns the allocated container IP.
func Setup(ctx context.Context, containerID, netNSPath string, ports []network.PortMapping, opts *network.SetupOptions, log *slog.Logger) (string, error) {
	internal := opts != nil && opts.Internal
	// 1. Bridge
	if err := ensureBridge(log); err != nil {
		return "", fmt.Errorf("bridge: %w", err)
	}

	// 2. IPAM
	_, gw := activeNetConf()
	ip, err := getIPAM().allocate(containerID)
	if err != nil {
		return "", fmt.Errorf("ipam: %w", err)
	}
	log.Debug("ip allocated", "container", containerID, "ip", ip)

	// 3. Veth + netns plumbing
	hostVeth, peerVeth := vethNames(containerID)
	// Best-effort: drop stale interfaces from a crashed run or older naming that
	// could leave RTM_NEWLINK failing with EEXIST.
	_ = deleteLink(hostVeth)
	_ = deleteLink(peerVeth)
	if err := createVethPair(hostVeth, peerVeth, log); err != nil {
		getIPAM().release(containerID) //nolint:errcheck
		return "", fmt.Errorf("veth: %w", err)
	}

	if err := attachVethToBridge(hostVeth, BridgeName, log); err != nil {
		deleteLink(hostVeth) //nolint:errcheck
		getIPAM().release(containerID)
		return "", fmt.Errorf("attach bridge: %w", err)
	}

	if err := moveVethToNetNS(peerVeth, netNSPath, ip, gw, log); err != nil {
		deleteLink(hostVeth) //nolint:errcheck
		getIPAM().release(containerID)
		return "", fmt.Errorf("move veth: %w", err)
	}

	// 4. Loopback inside netns
	if err := setLoUp(netNSPath); err != nil {
		// Non-fatal — container still has eth0
		log.Warn("set lo up", "container", containerID, "err", err)
	}

	// 5. Port mappings
	if len(ports) > 0 {
		if err := addPortMappings(containerID, ip, ports, log); err != nil {
			// Roll back host networking so we do not leave a running container without published ports
			// while the user believes -p succeeded.
			deleteLink(hostVeth) //nolint:errcheck
			getIPAM().release(containerID)
			return "", fmt.Errorf("port mappings: %w", err)
		}
	}

	// 6. Compose internal: block IPv4 egress outside the bridge CIDR (nft forward).
	if internal {
		if err := addInternalEgressBlock(containerID, ip, log); err != nil {
			removePortMappings(containerID, log) //nolint:errcheck
			deleteLink(hostVeth)                 //nolint:errcheck
			getIPAM().release(containerID)
			return "", fmt.Errorf("internal network egress: %w", err)
		}
	}

	return ip, nil
}

// Teardown removes all network resources for a container:
//   - releases its IPAM allocation
//   - deletes the host-side veth (peer disappears with the netns)
//   - removes nftables port-mapping rules
//   - removes compose internal egress filter rules when present
func Teardown(ctx context.Context, containerID string, log *slog.Logger) error {
	var errs []error

	if err := removeInternalEgressBlock(containerID, log); err != nil {
		errs = append(errs, fmt.Errorf("remove internal filter: %w", err))
	}

	if err := removePortMappings(containerID, log); err != nil {
		errs = append(errs, fmt.Errorf("remove portmap: %w", err))
	}

	hostVeth, _ := vethNames(containerID)
	if err := deleteLink(hostVeth); err != nil && !isNotExist(err) {
		errs = append(errs, fmt.Errorf("delete veth: %w", err))
	}

	if err := getIPAM().release(containerID); err != nil {
		errs = append(errs, fmt.Errorf("ipam release: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown %s: %v", containerID, errs)
	}
	return nil
}

// ─── Bridge ───────────────────────────────────────────────────────────────────

var bridgeOnce sync.Once
var bridgeErr error

// ensureBridge creates nyxbr0 if it doesn't exist and brings it up with the gateway IP.
func ensureBridge(log *slog.Logger) error {
	bridgeOnce.Do(func() {
		bridgeErr = createBridge(log)
	})
	if bridgeErr != nil {
		// Reset so next call retries.
		bridgeOnce = sync.Once{}
		return bridgeErr
	}
	tryEnableIPv4Forwarding(log)
	if err := ensureContainerEgressNAT(log); err != nil {
		log.Warn("container egress nat", "err", err)
	}
	return nil
}

func createBridge(log *slog.Logger) error {
	fd, err := unixSocket()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	// Check if bridge already exists.
	if linkExists(BridgeName) {
		log.Debug("bridge already exists", "name", BridgeName)
		return ensureBridgeIP(fd)
	}

	// RTM_NEWLINK: create bridge interface.
	req := newNetlinkMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_EXCL|unix.NLM_F_ACK)
	ifinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	req.addAttrString(unix.IFLA_IFNAME, BridgeName)
	req.addAttrUint32(unix.IFLA_MTU, MTU)

	// IFLA_LINKINFO → IFLA_INFO_KIND = "bridge"
	linkinfo := newNestedAttr(unix.IFLA_LINKINFO)
	linkinfo.addAttrString(unix.IFLA_INFO_KIND, "bridge")
	req.addNested(linkinfo)

	if err := netlinkDo(fd, req); err != nil {
		return fmt.Errorf("create bridge %s: %w", BridgeName, err)
	}
	log.Info("bridge created", "name", BridgeName)

	if err := ensureBridgeIP(fd); err != nil {
		return err
	}

	// Bring bridge up.
	return linkSetUp(fd, BridgeName)
}

// ensureBridgeIP assigns the gateway address to nyxbr0 if not already set.
func ensureBridgeIP(fd int) error {
	cidr, gwStr := activeNetConf()
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("bridge subnet: %w", err)
	}
	gwIP := net.ParseIP(gwStr).To4()
	if gwIP == nil {
		return fmt.Errorf("bridge gateway: invalid IPv4 %q", gwStr)
	}
	return addAddr(fd, BridgeName, gwIP, subnet)
}

// ─── Veth pair ────────────────────────────────────────────────────────────────

func createVethPair(host, peer string, log *slog.Logger) error {
	fd, err := unixSocket()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	req := newNetlinkMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_EXCL|unix.NLM_F_ACK)
	ifinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	req.addAttrString(unix.IFLA_IFNAME, host)
	req.addAttrUint32(unix.IFLA_MTU, MTU)

	// IFLA_LINKINFO → IFLA_INFO_KIND = "veth" → IFLA_INFO_DATA → VETH_INFO_PEER
	linkinfo := newNestedAttr(unix.IFLA_LINKINFO)
	linkinfo.addAttrString(unix.IFLA_INFO_KIND, "veth")
	infoData := newNestedAttr(unix.IFLA_INFO_DATA)
	peerInfo := newNestedAttr(vethInfoPeer)
	peerIfinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC}
	peerInfo.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&peerIfinfo))[:])
	peerInfo.addAttrString(unix.IFLA_IFNAME, peer)
	peerInfo.addAttrUint32(unix.IFLA_MTU, MTU)
	infoData.addNested(peerInfo)
	linkinfo.addNested(infoData)
	req.addNested(linkinfo)

	if err := netlinkDo(fd, req); err != nil {
		return fmt.Errorf("create veth %s/%s: %w", host, peer, err)
	}
	log.Debug("veth pair created", "host", host, "peer", peer)
	return nil
}

// attachVethToBridge sets host veth's master to the bridge and brings it up.
func attachVethToBridge(vethName, bridgeName string, log *slog.Logger) error {
	fd, err := unixSocket()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	bridgeIdx, err := linkIndex(bridgeName)
	if err != nil {
		return fmt.Errorf("bridge index: %w", err)
	}

	// RTM_SETLINK: set master
	req := newNetlinkMsg(unix.RTM_SETLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	vethIdx, err := linkIndex(vethName)
	if err != nil {
		return err
	}
	ifinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(vethIdx)}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	req.addAttrUint32(unix.IFLA_MASTER, uint32(bridgeIdx))
	if err := netlinkDo(fd, req); err != nil {
		return fmt.Errorf("set master: %w", err)
	}

	return linkSetUp(fd, vethName)
}

// moveVethToNetNS moves peer veth into the container's network namespace,
// assigns it IP address + default route, and brings it up.
func moveVethToNetNS(peerVeth, netNSPath, containerIP, gateway string, log *slog.Logger) error {
	// Open the target netns fd.
	nsFd, err := unix.Open(netNSPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open netns %s: %w", netNSPath, err)
	}
	defer unix.Close(nsFd)

	fd, err := unixSocket()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	// Move peer into netns via RTM_NEWLINK + IFLA_NET_NS_FD.
	peerIdx, err := linkIndex(peerVeth)
	if err != nil {
		return err
	}
	req := newNetlinkMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	ifinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(peerIdx)}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	req.addAttrUint32(unix.IFLA_NET_NS_FD, uint32(nsFd))
	req.addAttrString(unix.IFLA_IFNAME, "eth0") // rename to eth0 inside netns
	if err := netlinkDo(fd, req); err != nil {
		return fmt.Errorf("move peer to netns: %w", err)
	}

	// Now configure inside the netns.
	return inNetNS(netNSPath, func() error {
		fd2, err := unixSocket()
		if err != nil {
			return err
		}
		defer unix.Close(fd2)

		ip := net.ParseIP(containerIP).To4()
		cidr, _ := activeNetConf()
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("container subnet: %w", err)
		}

		if err := addAddr(fd2, "eth0", ip, subnet); err != nil {
			return fmt.Errorf("add addr to eth0: %w", err)
		}
		if err := linkSetUp(fd2, "eth0"); err != nil {
			return fmt.Errorf("eth0 up: %w", err)
		}

		gw := net.ParseIP(gateway).To4()
		return addDefaultRoute(fd2, gw)
	})
}

// setLoUp brings the loopback interface up inside a netns.
func setLoUp(netNSPath string) error {
	return inNetNS(netNSPath, func() error {
		fd, err := unixSocket()
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		return linkSetUp(fd, "lo")
	})
}

// ─── IPAM ─────────────────────────────────────────────────────────────────────

var (
	ipamOnce sync.Once
	ipamInst *ipam
)

// getIPAM returns the process-wide file-backed IP allocator.
// Default state dir is /var/lib/nyxd/ipam; override with NYXD_IPAM_DIR.
// If the default is not writable (e.g. CI without root), falls back to $TMPDIR/nyxd-ipam.
func getIPAM() *ipam {
	ipamOnce.Do(func() {
		dir := strings.TrimSpace(os.Getenv("NYXD_IPAM_DIR"))
		if dir == "" {
			def := "/var/lib/nyxd/ipam"
			if err := os.MkdirAll(def, 0o700); err == nil {
				dir = def
			} else {
				dir = filepath.Join(os.TempDir(), "nyxd-ipam")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					panic("ipam dir: cannot create " + dir + ": " + err.Error())
				}
			}
		} else {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				panic("ipam dir: cannot create " + dir + ": " + err.Error())
			}
		}
		cidr, gw := activeNetConf()
		ipamInst = newIPAM(dir, cidr, gw)
	})
	return ipamInst
}

// ipam is a simple file-backed IP allocator.
// Uses one file per allocated IP: /ipamDir/<hex-ip> → containerID
// Thread-safe, crash-safe (atomic writes).
type ipam struct {
	dir    string
	mu     sync.Mutex
	subnet *net.IPNet
	gwU    uint32 // gateway host address (excluded from allocation)
	baseU  uint32 // network address as uint32
	bcU    uint32 // broadcast as uint32
}

func newIPAM(dir, cidr, gwStr string) *ipam {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic("ipam dir: " + err.Error())
	}
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic("ipam cidr: " + err.Error())
	}
	ip4 := subnet.IP.To4()
	if ip4 == nil {
		panic("ipam cidr: IPv4 only")
	}
	gw := net.ParseIP(strings.TrimSpace(gwStr)).To4()
	if gw == nil {
		panic("ipam gateway: invalid IPv4 " + gwStr)
	}
	gwU := ipToU32(gw)
	base := ipToU32(ip4)
	maskIP := net.IP(subnet.Mask).To4()
	if maskIP == nil {
		panic("ipam cidr: IPv4 mask only")
	}
	mask := binary.BigEndian.Uint32(maskIP)
	broadcast := base | ^mask
	if gwU <= base || gwU >= broadcast {
		panic(fmt.Sprintf("ipam: gateway %s not inside subnet %s", gwStr, cidr))
	}
	// Need at least one assignable host besides gateway.
	if broadcast <= base+1 {
		panic(fmt.Sprintf("ipam cidr: no room in %s", cidr))
	}
	return &ipam{
		dir:    dir,
		subnet: subnet,
		gwU:    gwU,
		baseU:  base,
		bcU:    broadcast,
	}
}

func (a *ipam) allocate(containerID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	used := a.usedSet()
	for cand := a.baseU + 1; cand < a.bcU; cand++ {
		if cand == a.gwU {
			continue
		}
		if used[cand] {
			continue
		}
		ip := u32ToIP(cand)
		path := filepath.Join(a.dir, fmt.Sprintf("%08x", cand))
		if err := os.WriteFile(path, []byte(containerID), 0o600); err != nil {
			return "", fmt.Errorf("ipam write: %w", err)
		}
		return ip.String(), nil
	}
	return "", fmt.Errorf("ipam exhausted: subnet %s is full", a.subnet)
}

func (a *ipam) release(containerID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(a.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if string(data) == containerID {
			return os.Remove(path)
		}
	}
	return nil // already released
}

func (a *ipam) usedSet() map[uint32]bool {
	used := make(map[uint32]bool)
	entries, _ := os.ReadDir(a.dir)
	for _, e := range entries {
		var v uint32
		fmt.Sscanf(e.Name(), "%08x", &v)
		if v != 0 {
			used[v] = true
		}
	}
	return used
}

// ─── Port mappings via nftables ───────────────────────────────────────────────

var portMapNftMu sync.Mutex

// tryEnsureNATOutputChain adds the output NAT hook if missing (older nyxd
// installs only created prerouting/postrouting).
func tryEnsureNATOutputChain() error {
	if err := ensureNftTable(); err != nil {
		return err
	}
	return withTimeout(30*time.Second, func() error {
		cmd := exec.Command("/usr/sbin/nft", "add", "chain", "ip", "nyxd-nat", "output",
			"{", "type", "nat", "hook", "output", "priority", "-100", ";", "policy", "accept", ";", "}")
		out, err := cmd.CombinedOutput()
		if err != nil {
			s := strings.ToLower(strings.TrimSpace(string(out)))
			if strings.Contains(s, "file exists") || strings.Contains(s, "exists") {
				return nil
			}
			return fmt.Errorf("nft add chain output: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

// addPortMappings installs PREROUTING DNAT + POSTROUTING MASQUERADE rules
// using nft via the nftables netlink API (no nft binary required).
// For simplicity we shell out to nft here — pure netlink nftables is 1000+ lines.
// The nft binary is tiny (part of nftables package) and has no CVE history.
func addPortMappings(containerID, containerIP string, ports []network.PortMapping, log *slog.Logger) error {
	portMapNftMu.Lock()
	defer portMapNftMu.Unlock()

	if err := tryEnsureNATOutputChain(); err != nil {
		return err
	}
	tag := portmapCommentPrefix(containerID)
	var errs []error
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}

		rules := []struct {
			chain string
			rule  string
		}{
			{"prerouting", fmt.Sprintf(
				"nft add rule ip nyxd-nat prerouting %s dport %d dnat to %s:%d comment %s",
				proto, p.HostPort, containerIP, p.ContainerPort, strconv.Quote(fmt.Sprintf("%s-p%d-0", tag, p.HostPort)),
			)},
			{"output", fmt.Sprintf(
				"nft add rule ip nyxd-nat output %s dport %d dnat to %s:%d comment %s",
				proto, p.HostPort, containerIP, p.ContainerPort, strconv.Quote(fmt.Sprintf("%s-p%d-1", tag, p.HostPort)),
			)},
			{"postrouting", fmt.Sprintf(
				"nft add rule ip nyxd-nat postrouting ip daddr %s %s dport %d masquerade comment %s",
				containerIP, proto, p.ContainerPort, strconv.Quote(fmt.Sprintf("%s-p%d-2", tag, p.HostPort)),
			)},
		}

		for _, r := range rules {
			if err := runNft(r.rule); err != nil {
				log.Warn("nft rule", "chain", r.chain, "err", err)
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("nft: %v", errs)
	}
	return nil
}

func removePortMappings(containerID string, log *slog.Logger) error {
	portMapNftMu.Lock()
	defer portMapNftMu.Unlock()

	prefix := portmapCommentPrefix(containerID)
	var errs []error
	for _, chain := range []string{"prerouting", "output", "postrouting"} {
		if err := nftDeleteRulesWithCommentPrefix("nyxd-nat", chain, prefix); err != nil {
			errs = append(errs, fmt.Errorf("chain %s: %w", chain, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func runNft(rule string) error {
	if err := ensureNftTable(); err != nil {
		return err
	}
	return withTimeout(30*time.Second, func() error {
		cmd := exec.Command("/bin/sh", "-c", rule)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

var nftTableOnce sync.Once

func ensureNftTable() error {
	var initErr error
	nftTableOnce.Do(func() {
		// Postrouting SNAT for container→internet is added by [ensureContainerEgressNAT]
		// (ip saddr <subnet> oifname != bridge masquerade). A rule matching only
		// oifname "nyxbr0" never sees forwarded WAN-bound traffic, so ping/wget to
		// public addresses would fail without the egress rule.
		rules := `
table ip nyxd-nat {
  chain prerouting {
    type nat hook prerouting priority dstnat; policy accept;
  }
  chain output {
    type nat hook output priority -100; policy accept;
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
  }
}
`
		f, err := os.CreateTemp("", "nyxd-nft-*.rules")
		if err != nil {
			initErr = err
			return
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(rules); err != nil {
			initErr = err
			return
		}
		if err := f.Close(); err != nil {
			initErr = err
			return
		}
		cmd := exec.Command("/usr/sbin/nft", "-f", f.Name())
		out, err := cmd.CombinedOutput()
		if err != nil {
			initErr = fmt.Errorf("nft init: %w: %s", err, strings.TrimSpace(string(out)))
		}
	})
	return initErr
}

// egressNATComment tags the SNAT rule for traffic leaving the host toward non-bridge
// interfaces (typically the default route / internet).
const egressNATComment = "nyxd-container-egress"

// ensureContainerEgressNAT adds a postrouting masquerade rule for the container
// subnet when traffic exits via an interface other than the nyxd bridge. Safe to
// call repeatedly; skips if a rule with [egressNATComment] is already present.
// Fixes upgrades from older nyxd that used a non-functional oifname-only masquerade.
func ensureContainerEgressNAT(log *slog.Logger) error {
	if err := ensureNftTable(); err != nil {
		return err
	}
	cidr, _ := activeNetConf()
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return fmt.Errorf("egress nat: subnet %q: %w", cidr, err)
	}

	portMapNftMu.Lock()
	defer portMapNftMu.Unlock()

	lines, err := nftListChainLines("nyxd-nat", "postrouting")
	if err != nil {
		return err
	}
	for _, ln := range lines {
		if strings.Contains(ln, egressNATComment) {
			return nil
		}
	}

	rule := fmt.Sprintf(
		`nft add rule ip nyxd-nat postrouting ip saddr %s oifname != %q masquerade comment %q`,
		cidr, BridgeName, egressNATComment,
	)
	if err := runNftUnlocked(rule); err != nil {
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "file exists") || strings.Contains(s, "exists") {
			return nil
		}
		return err
	}
	log.Info("container egress masquerade rule installed", "subnet", cidr, "bridge", BridgeName)
	return nil
}

// runNftUnlocked runs nft like [runNft] but does not call [ensureNftTable] (caller
// must hold [portMapNftMu] when appropriate to avoid races with [ensureNftTable]).
func runNftUnlocked(rule string) error {
	return withTimeout(30*time.Second, func() error {
		cmd := exec.Command("/bin/sh", "-c", rule)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

var nftFilterOnce sync.Once
var nftFilterInitErr error

func ensureNftFilterTable() error {
	nftFilterOnce.Do(func() {
		rules := `
table ip nyxd-filter {
  chain forward {
    type filter hook forward priority 0; policy accept;
  }
}
`
		f, err := os.CreateTemp("", "nyxd-nft-filter-*.rules")
		if err != nil {
			nftFilterInitErr = err
			return
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(rules); err != nil {
			nftFilterInitErr = err
			return
		}
		if err := f.Close(); err != nil {
			nftFilterInitErr = err
			return
		}
		cmd := exec.Command("/usr/sbin/nft", "-f", f.Name())
		out, err := cmd.CombinedOutput()
		if err != nil {
			nftFilterInitErr = fmt.Errorf("nft filter init: %w: %s", err, strings.TrimSpace(string(out)))
		}
	})
	return nftFilterInitErr
}

func addInternalEgressBlock(containerID, containerIP string, log *slog.Logger) error {
	if err := ensureNftFilterTable(); err != nil {
		return err
	}
	cidr, _ := activeNetConf()
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return fmt.Errorf("internal filter: subnet %q: %w", cidr, err)
	}

	portMapNftMu.Lock()
	defer portMapNftMu.Unlock()

	tag := internalEgressCommentPrefix(containerID)
	comment := fmt.Sprintf("%s-e0", tag)
	rule := fmt.Sprintf(
		`nft add rule ip nyxd-filter forward ip saddr %s ip daddr != %s drop comment %q`,
		containerIP, cidr, comment,
	)
	if err := runNftUnlocked(rule); err != nil {
		return err
	}
	log.Info("internal network egress blocked", "container", containerID, "ip", containerIP, "allow_cidr", cidr)
	return nil
}

func removeInternalEgressBlock(containerID string, log *slog.Logger) error {
	portMapNftMu.Lock()
	defer portMapNftMu.Unlock()

	prefix := internalEgressCommentPrefix(containerID)
	err := nftDeleteRulesWithCommentPrefix("nyxd-filter", "forward", prefix)
	if err != nil {
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "could not find") || strings.Contains(s, "no such file") ||
			strings.Contains(s, "does not exist") {
			return nil
		}
		return err
	}
	return nil
}

// ─── Netlink helpers ──────────────────────────────────────────────────────────

func unixSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return -1, fmt.Errorf("netlink socket: %w", err)
	}
	addr := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("netlink bind: %w", err)
	}
	return fd, nil
}

type nlMsg struct {
	buf []byte
	seq uint32
}

var nlSeq uint32

func newNetlinkMsg(typ, flags uint16) *nlMsg {
	nlSeq++
	m := &nlMsg{seq: nlSeq}
	hdr := unix.NlMsghdr{
		Type:  typ,
		Flags: flags,
		Seq:   nlSeq,
	}
	m.buf = (*[unix.SizeofNlMsghdr]byte)(unsafe.Pointer(&hdr))[:]
	return m
}

func (m *nlMsg) addBytes(b []byte) {
	m.buf = append(m.buf, b...)
	// Align to 4 bytes.
	for len(m.buf)%4 != 0 {
		m.buf = append(m.buf, 0)
	}
}

func (m *nlMsg) addAttrString(typ int, s string) {
	b := append([]byte(s), 0)
	m.addAttr(typ, b)
}

func (m *nlMsg) addAttrUint32(typ int, v uint32) {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	m.addAttr(typ, b)
}

func (m *nlMsg) addAttr(typ int, data []byte) {
	alen := unix.SizeofRtAttr + len(data)
	attr := unix.RtAttr{Len: uint16(alen), Type: uint16(typ)}
	m.buf = append(m.buf, (*[unix.SizeofRtAttr]byte)(unsafe.Pointer(&attr))[:]...)
	m.buf = append(m.buf, data...)
	for len(m.buf)%4 != 0 {
		m.buf = append(m.buf, 0)
	}
}

type nestedAttr struct {
	typ int
	buf []byte
}

func newNestedAttr(typ int) *nestedAttr { return &nestedAttr{typ: typ} }

func (n *nestedAttr) addAttrString(typ int, s string) {
	tmp := &nlMsg{}
	tmp.addAttrString(typ, s)
	n.buf = append(n.buf, tmp.buf...)
}

func (n *nestedAttr) addAttrUint32(typ int, v uint32) {
	tmp := &nlMsg{}
	tmp.addAttrUint32(typ, v)
	n.buf = append(n.buf, tmp.buf...)
}

func (n *nestedAttr) addBytes(b []byte) {
	n.buf = append(n.buf, b...)
	for len(n.buf)%4 != 0 {
		n.buf = append(n.buf, 0)
	}
}

func (n *nestedAttr) addNested(child *nestedAttr) {
	n.buf = append(n.buf, child.serialize()...)
}

func (n *nestedAttr) serialize() []byte {
	alen := unix.SizeofRtAttr + len(n.buf)
	attr := unix.RtAttr{Len: uint16(alen), Type: uint16(n.typ) | unix.NLA_F_NESTED}
	out := (*[unix.SizeofRtAttr]byte)(unsafe.Pointer(&attr))[:]
	out = append(out, n.buf...)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

func (m *nlMsg) addNested(n *nestedAttr) {
	m.buf = append(m.buf, n.serialize()...)
}

// netlinkMessage is one RTNETLINK datagram after the generic header.
type netlinkMessage struct {
	Header unix.NlMsghdr
	Data   []byte
}

func parseNetlinkMessages(b []byte) ([]netlinkMessage, error) {
	var out []netlinkMessage
	off := 0
	for off+unix.NLMSG_HDRLEN <= len(b) {
		hdr := (*unix.NlMsghdr)(unsafe.Pointer(&b[off]))
		msgLen := int(hdr.Len)
		if msgLen < unix.NLMSG_HDRLEN || off+msgLen > len(b) {
			return nil, fmt.Errorf("invalid netlink message length %d", msgLen)
		}
		data := append([]byte(nil), b[off+unix.NLMSG_HDRLEN:off+msgLen]...)
		out = append(out, netlinkMessage{Header: *hdr, Data: data})
		off += nlmsgAlign(msgLen)
	}
	return out, nil
}

func nlmsgAlign(l int) int {
	return (l + unix.NLMSG_ALIGNTO - 1) &^ (unix.NLMSG_ALIGNTO - 1)
}

func netlinkDo(fd int, req *nlMsg) error {
	// Fix up length field in header.
	binary.NativeEndian.PutUint32(req.buf[:4], uint32(len(req.buf)))

	if err := unix.Sendto(fd, req.buf, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("nl send: %w", err)
	}

	rbuf := make([]byte, 4096)
	for {
		n, _, err := unix.Recvfrom(fd, rbuf, 0)
		if err != nil {
			return fmt.Errorf("nl recv: %w", err)
		}
		msgs, err := parseNetlinkMessages(rbuf[:n])
		if err != nil {
			return fmt.Errorf("nl parse: %w", err)
		}
		for _, msg := range msgs {
			if msg.Header.Type == unix.NLMSG_ERROR {
				if len(msg.Data) < 4 {
					return fmt.Errorf("nl error: short ack")
				}
				errno := int32(binary.NativeEndian.Uint32(msg.Data[:4]))
				if errno == 0 {
					return nil // ACK
				}
				return fmt.Errorf("nl error: %w", syscall.Errno(-errno))
			}
			if msg.Header.Type == unix.NLMSG_DONE {
				return nil
			}
		}
	}
}

// linkSetUp brings a named interface up (IFF_UP).
func linkSetUp(fd int, name string) error {
	req := newNetlinkMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	idx, err := linkIndex(name)
	if err != nil {
		return err
	}
	ifinfo := unix.IfInfomsg{
		Family: unix.AF_UNSPEC,
		Index:  int32(idx),
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
	}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	return netlinkDo(fd, req)
}

// deleteLink removes a network interface by name.
func deleteLink(name string) error {
	fd, err := unixSocket()
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	idx, err := linkIndex(name)
	if err != nil {
		return err
	}
	req := newNetlinkMsg(unix.RTM_DELLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	ifinfo := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(idx)}
	req.addBytes((*[unix.SizeofIfInfomsg]byte)(unsafe.Pointer(&ifinfo))[:])
	return netlinkDo(fd, req)
}

// addAddr assigns an IPv4 address+prefix to an interface.
func addAddr(fd int, name string, ip net.IP, subnet *net.IPNet) error {
	idx, err := linkIndex(name)
	if err != nil {
		return err
	}
	ones, _ := subnet.Mask.Size()

	req := newNetlinkMsg(unix.RTM_NEWADDR, unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_REPLACE|unix.NLM_F_ACK)
	ifaddr := unix.IfAddrmsg{
		Family:    unix.AF_INET,
		Prefixlen: uint8(ones),
		Scope:     unix.RT_SCOPE_UNIVERSE,
		Index:     uint32(idx),
	}
	req.addBytes((*[unix.SizeofIfAddrmsg]byte)(unsafe.Pointer(&ifaddr))[:])
	req.addAttr(unix.IFA_LOCAL, ip.To4())
	req.addAttr(unix.IFA_ADDRESS, ip.To4())
	return netlinkDo(fd, req)
}

// addDefaultRoute adds a 0.0.0.0/0 route via gateway on the interface.
func addDefaultRoute(fd int, gw net.IP) error {
	req := newNetlinkMsg(unix.RTM_NEWROUTE, unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_REPLACE|unix.NLM_F_ACK)
	rtmsg := unix.RtMsg{
		Family:   unix.AF_INET,
		Table:    unix.RT_TABLE_MAIN,
		Protocol: unix.RTPROT_BOOT,
		Scope:    unix.RT_SCOPE_UNIVERSE,
		Type:     unix.RTN_UNICAST,
	}
	req.addBytes((*[unix.SizeofRtMsg]byte)(unsafe.Pointer(&rtmsg))[:])
	req.addAttr(unix.RTA_GATEWAY, gw.To4())
	return netlinkDo(fd, req)
}

// linkIndex returns the interface index for a named interface.
func linkIndex(name string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("interface %s: %w", name, err)
	}
	return iface.Index, nil
}

// linkExists returns true if the named interface exists.
func linkExists(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

// ─── netns helpers ────────────────────────────────────────────────────────────

// inNetNS executes fn inside the network namespace at nsPath.
// Uses runtime.LockOSThread to prevent goroutine migration between threads.
func inNetNS(nsPath string, fn func() error) error {
	// We must lock the OS thread because namespace operations are per-thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save current netns.
	origNS, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("open current netns: %w", err)
	}
	defer origNS.Close()

	// Open target netns.
	targetNS, err := os.Open(nsPath)
	if err != nil {
		return fmt.Errorf("open target netns %s: %w", nsPath, err)
	}
	defer targetNS.Close()

	// Enter target netns via setns(2).
	if err := setns(targetNS.Fd(), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("setns %s: %w", nsPath, err)
	}

	// Execute fn inside the netns.
	fnErr := fn()

	// Always restore original netns.
	if err := setns(origNS.Fd(), unix.CLONE_NEWNET); err != nil {
		// This is fatal — we're stuck in the wrong netns.
		panic(fmt.Sprintf("restore netns: %v", err))
	}

	return fnErr
}

func setns(fd uintptr, nstype int) error {
	_, _, errno := unix.RawSyscall(unix.SYS_SETNS, fd, uintptr(nstype), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// ─── IP utilities ─────────────────────────────────────────────────────────────

func ipToU32(ip net.IP) uint32 {
	ip = ip.To4()
	return binary.BigEndian.Uint32(ip)
}

func u32ToIP(v uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return net.IP(b)
}

func vethNames(containerID string) (host, peer string) {
	// Linux IFNAMSIZ is 16 bytes including NUL — 15 printable chars max.
	// Hash the full container ID so similar IDs (e.g. two nginx-alpine-* refs
	// that share an 8-byte prefix) never collide on interface names.
	sum := sha256.Sum256([]byte(containerID))
	short := hex.EncodeToString(sum[:4]) // 8 hex chars
	return "veth" + short + "h", "veth" + short + "p"
}

func isNotExist(err error) bool {
	return err == unix.ENODEV || err == unix.ENOENT || os.IsNotExist(err)
}

// ensure timeout on slow netns ops.
func withTimeout(d time.Duration, fn func() error) error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		return fmt.Errorf("timeout after %s", d)
	}
}
