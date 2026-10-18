//go:build linux

package native

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"golang.org/x/sys/unix"
)

// These tests run in a private user + network namespace (nsrun), where the
// test is root of its own network stack: bridge, veth, routes and nftables
// rules are real.

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// inNS runs the calling test in fresh namespaces with its own IPAM dir.
func inNS(t *testing.T) bool {
	t.Helper()
	if _, err := os.Stat("/usr/sbin/nft"); err != nil {
		t.Skip("nft not installed")
	}
	if !nsrun.Run(t) {
		return false
	}
	t.Setenv("NYXD_IPAM_DIR", t.TempDir())
	return true
}

// newNetNS creates a network namespace bind-mounted on a file, like the
// supervisor does for a container.
func newNetNS(t *testing.T) string {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	defer orig.Close()
	p := filepath.Join(t.TempDir(), "netns")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		t.Fatal(err)
	}
	mErr := unix.Mount("/proc/thread-self/ns/net", p, "", unix.MS_BIND, "")
	if err := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); err != nil {
		t.Fatal(err)
	}
	if mErr != nil {
		t.Fatal(mErr)
	}
	t.Cleanup(func() { _ = unix.Unmount(p, unix.MNT_DETACH) })
	return p
}

// fakeNft installs an nft wrapper that fails (or hangs) when its arguments
// contain match, and runs the real nft otherwise.
func fakeNft(t *testing.T, match, mode string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"` + match + `"*)
	[ "` + mode + `" = hang ] && exec sleep 30
	echo "fake nft: ` + mode + `" >&2
	exit 1 ;;
esac
exec /usr/sbin/nft "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "nft"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := nftBin
	nftBin = filepath.Join(dir, "nft")
	t.Cleanup(func() { nftBin = old })
	t.Setenv("PATH", dir+":"+os.Getenv("PATH")) // runNft uses `sh -c "nft ..."`
}

func ruleset(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("/usr/sbin/nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v: %s", err, out)
	}
	return string(out)
}

func addrsIn(t *testing.T, nsPath, ifname string) []string {
	t.Helper()
	var out []string
	err := inNetNS(nsPath, func() error {
		ifc, err := net.InterfaceByName(ifname)
		if err != nil {
			return err
		}
		as, err := ifc.Addrs()
		for _, a := range as {
			out = append(out, a.String())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNativeBridgeIsolation(t *testing.T) {
	if !inNS(t) {
		return
	}
	ctx := context.Background()
	m := NewManager(quiet)
	ns1, ns2 := newNetNS(t), newNetNS(t)
	optA := &network.SetupOptions{Bridge: "nyxbr-a", Subnet: "10.91.0.0/16", Gateway: "10.91.0.1"}
	optB := &network.SetupOptions{Bridge: "nyxbr-b", Subnet: "10.92.0.0/16", Gateway: "10.92.0.1"}
	if _, err := m.Setup(ctx, "a", ns1, nil, optA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Setup(ctx, "b", ns2, nil, optB); err != nil {
		t.Fatal(err)
	}
	rs := ruleset(t)
	for _, want := range []string{
		`nyxd-bridge-iso-nyxbr-a-nyxbr-b`,
		`nyxd-bridge-iso-nyxbr-b-nyxbr-a`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, rs)
		}
	}
	// Second ensure is idempotent (existing comments → continue).
	ensureBridgeIsolation(bridgeConf{Bridge: "nyxbr-a", Subnet: "10.91.0.0/16", Gateway: "10.91.0.1"}, quiet)
	if err := m.Teardown(ctx, "a", ns1); err != nil {
		t.Fatal(err)
	}
	if err := m.Teardown(ctx, "b", ns2); err != nil {
		t.Fatal(err)
	}
	// No host veth / IPAM / bridge bookkeeping left behind.
	for _, id := range []string{"a", "b"} {
		if h, _ := vethNames(id); linkExists(h) {
			t.Errorf("%s: host veth leaked", id)
		}
		if _, ok := forgetBridge(id); ok {
			t.Errorf("%s: containerBridge entry leaked", id)
		}
	}
	if m.IP("a") != "" || m.IP("b") != "" {
		t.Fatalf("manager still tracks IPs: %v", m.Allocations())
	}
}

func TestNativeSetupTeardown(t *testing.T) {
	if !inNS(t) {
		return
	}
	ctx := context.Background()
	m := NewManager(quiet)
	if err := m.EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureNetwork(); err != nil { // idempotent
		t.Fatal(err)
	}
	br, err := net.InterfaceByName(BridgeName)
	if err != nil || br.Flags&net.FlagUp == 0 {
		t.Fatalf("bridge: %v %v", br, err)
	}
	if as, _ := br.Addrs(); len(as) == 0 || as[0].String() != "10.88.0.1/16" {
		t.Fatalf("bridge addrs %v", as)
	}

	ns1, ns2 := newNetNS(t), newNetNS(t)
	ports := []network.PortMapping{{HostPort: 18080, ContainerPort: 80}, {HostPort: 15353, ContainerPort: 53, Protocol: "udp"}}
	ip1, err := m.Setup(ctx, "web", ns1, ports, &network.SetupOptions{Internal: true})
	if err != nil {
		t.Fatal(err)
	}
	ip2, err := m.Setup(ctx, "db", ns2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ip1 != "10.88.0.2" || ip2 != "10.88.0.3" {
		t.Fatalf("ips %s %s", ip1, ip2)
	}
	if m.IP("web") != ip1 || len(m.Allocations()) != 2 {
		t.Fatalf("allocations %v", m.Allocations())
	}
	if got := addrsIn(t, ns1, "eth0"); len(got) == 0 || got[0] != "10.88.0.2/16" {
		t.Fatalf("eth0 in container: %v", got)
	}
	if got := addrsIn(t, ns1, "lo"); len(got) == 0 {
		t.Fatal("lo has no address (not up)")
	}
	rs := ruleset(t)
	for _, want := range []string{
		"tcp dport 18080 dnat to 10.88.0.2:80", "udp dport 15353 dnat to 10.88.0.2:53",
		portmapCommentPrefix("web"), internalEgressCommentPrefix("web"), egressNATComment,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, rs)
		}
	}
	host, _ := vethNames("web")
	if !linkExists(host) {
		t.Fatal("host veth missing")
	}

	if err := m.Teardown(ctx, "web", ns1); err != nil {
		t.Fatal(err)
	}
	if err := m.Teardown(ctx, "web", ns1); err != nil { // idempotent
		t.Fatal(err)
	}
	if linkExists(host) || m.IP("web") != "" {
		t.Fatal("web not torn down")
	}
	rs = ruleset(t)
	if strings.Contains(rs, portmapCommentPrefix("web")) || strings.Contains(rs, internalEgressCommentPrefix("web")) {
		t.Fatalf("rules left behind:\n%s", rs)
	}
	// The freed address is reused.
	if ip, err := m.Setup(ctx, "web2", newNetNS(t), nil, nil); err != nil || ip != "10.88.0.2" {
		t.Fatalf("reuse: %s %v", ip, err)
	}
	if err := m.Teardown(ctx, "db", ns2); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSetupRollback(t *testing.T) {
	if !inNS(t) {
		return
	}
	ctx := context.Background()
	check := func(id string) {
		t.Helper()
		if h, _ := vethNames(id); linkExists(h) {
			t.Errorf("%s: host veth left behind", id)
		}
		if ip, _ := getIPAM().allocate("probe"); ip != "10.88.0.2" {
			t.Errorf("%s: address not released (next is %s)", id, ip)
		}
		_ = getIPAM().release("probe")
	}

	if _, err := Setup(ctx, "badns", "/nonexistent/netns", nil, nil, quiet); err == nil || !strings.Contains(err.Error(), "move veth") {
		t.Fatalf("bad netns: %v", err)
	}
	check("badns")

	fakeNft(t, "dport 18081", "fail")
	_, err := Setup(ctx, "pm", newNetNS(t), []network.PortMapping{{HostPort: 18081, ContainerPort: 80}}, nil, quiet)
	if err == nil || !strings.Contains(err.Error(), "port mappings") {
		t.Fatalf("port mapping failure: %v", err)
	}
	check("pm")

	fakeNft(t, "nyxd-filter forward", "fail")
	_, err = Setup(ctx, "int", newNetNS(t), nil, &network.SetupOptions{Internal: true}, quiet)
	if err == nil || !strings.Contains(err.Error(), "internal network egress") {
		t.Fatalf("internal failure: %v", err)
	}
	check("int")
}

func TestNativeVethExists(t *testing.T) {
	if !inNS(t) {
		return
	}
	// A leftover interface under the peer name is removed before creating the pair.
	_, peer := vethNames("stale")
	if err := createVethPair(peer, "tmpx", quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(context.Background(), "stale", newNetNS(t), nil, nil, quiet); err != nil {
		t.Fatal(err)
	}
	// Creating an existing pair fails cleanly.
	h, p := vethNames("stale")
	if err := createVethPair(h, p+"2", quiet); err == nil {
		t.Fatal("duplicate veth accepted")
	}
	if err := attachVethToBridge("nope0", BridgeName, quiet); err == nil {
		t.Fatal("attach unknown veth")
	}
	if err := attachVethToBridge(h, "nobridge", quiet); err == nil {
		t.Fatal("attach to unknown bridge")
	}
	if err := moveVethToNetNS("nope0", newNetNS(t), "10.88.0.9", GatewayIP, ContainerSubnet, quiet); err == nil {
		t.Fatal("move unknown veth")
	}
}

func TestNativeNftMissing(t *testing.T) {
	if !inNS(t) {
		return
	}
	nftBin = "/nonexistent/nft"
	t.Setenv("PATH", "/nonexistent") // runNft: sh finds no nft either
	// The bridge still comes up; missing NAT is only logged.
	m := NewManager(quiet)
	if err := m.EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	_, err := m.Setup(context.Background(), "c", newNetNS(t), []network.PortMapping{{HostPort: 1, ContainerPort: 2}}, nil)
	if err == nil {
		t.Fatal("port mapping without nft succeeded")
	}
	if err := Teardown(context.Background(), "c", quiet); err == nil || !strings.Contains(err.Error(), "remove portmap") {
		t.Fatalf("teardown without nft: %v", err)
	}
	if err := ensureNftFilterTable(); err == nil {
		t.Fatal("filter table without nft")
	}
	if err := addInternalEgressBlock("c", "10.88.0.2", ContainerSubnet, quiet); err == nil {
		t.Fatal("internal block without nft")
	}
	if err := ensureContainerEgressNAT(defaultBridgeConf(), quiet); err == nil {
		t.Fatal("egress nat without nft")
	}
}

func TestNativeNftErrors(t *testing.T) {
	if !inNS(t) {
		return
	}
	// "exists" answers count as success.
	fakeNft(t, "add chain ip nyxd-nat output", "File exists")
	if err := tryEnsureNATOutputChain(); err != nil {
		t.Fatalf("existing output chain: %v", err)
	}
	fakeNft(t, "add chain ip nyxd-nat output", "permission denied")
	if err := tryEnsureNATOutputChain(); err == nil {
		t.Fatal("output chain failure ignored")
	}
	fakeNft(t, egressNATComment, "rule exists")
	if err := ensureContainerEgressNAT(defaultBridgeConf(), quiet); err != nil {
		t.Fatalf("existing egress rule: %v", err)
	}
	fakeNft(t, egressNATComment, "bad rule")
	if err := ensureContainerEgressNAT(defaultBridgeConf(), quiet); err == nil {
		t.Fatal("egress rule failure ignored")
	}
	fakeNft(t, "list chain ip nyxd-nat postrouting", "list failed")
	if err := ensureContainerEgressNAT(defaultBridgeConf(), quiet); err == nil {
		t.Fatal("list failure ignored")
	}
	if err := removePortMappings("x", quiet); err == nil {
		t.Fatal("remove with failing list succeeded")
	}
	fakeNft(t, "list chain ip nyxd-filter forward", "permission denied")
	if err := removeInternalEgressBlock("x", quiet); err == nil {
		t.Fatal("filter list failure ignored")
	}

	// A rule nft refuses to delete.
	fakeNft(t, "nothing", "fail") // reset to the real nft
	if err := addPortMappings("del", "10.88.0.5", []network.PortMapping{{HostPort: 18082, ContainerPort: 80}}, quiet); err != nil {
		t.Fatal(err)
	}
	fakeNft(t, "delete rule", "busy")
	if err := removePortMappings("del", quiet); err == nil || !strings.Contains(err.Error(), "handle") {
		t.Fatalf("delete failure: %v", err)
	}

	// Hung nft.
	nftTimeout = 200 * time.Millisecond
	t.Cleanup(func() { nftTimeout = 30 * time.Second })
	fakeNft(t, "add rule", "hang")
	if err := runNft("nft add rule ip nyxd-nat output tcp dport 1 accept"); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("hung nft: %v", err)
	}
}

func TestNativeBadConfig(t *testing.T) {
	if !inNS(t) {
		return
	}
	t.Setenv("NYXD_CONTAINER_SUBNET", "bogus")
	if err := NewManager(quiet).EnsureNetwork(); err == nil {
		t.Fatal("bad subnet accepted")
	}
	if err := ensureBridge(quiet); err == nil { // retried, fails again
		t.Fatal("bad subnet accepted on retry")
	}
	if err := ensureContainerEgressNAT(bridgeConf{Bridge: BridgeName, Subnet: "bogus", Gateway: GatewayIP}, quiet); err == nil {
		t.Fatal("egress nat with bad subnet")
	}
	if err := addInternalEgressBlock("c", "10.88.0.2", "bogus", quiet); err == nil {
		t.Fatal("internal block with bad subnet")
	}
	if err := createVethPair("cfgh", "cfgp", quiet); err != nil {
		t.Fatal(err)
	}
	if err := moveVethToNetNS("cfgp", newNetNS(t), "10.88.0.2", GatewayIP, "bogus", quiet); err == nil {
		t.Fatal("move with bad subnet")
	}
	if _, err := Setup(context.Background(), "c", newNetNS(t), nil, nil, quiet); err == nil || !strings.Contains(err.Error(), "bridge") {
		t.Fatalf("setup with bad subnet: %v", err)
	}
	t.Setenv("NYXD_CONTAINER_SUBNET", "")
	t.Setenv("NYXD_GATEWAY_IP", "not-an-ip")
	if err := ensureBridge(quiet); err == nil {
		t.Fatal("bad gateway accepted")
	}
}

func TestNativeIPAMDirs(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	// No NYXD_IPAM_DIR and /var/lib/nyxd not writable: falls back to $TMPDIR.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("NYXD_IPAM_DIR", "")
	resetIPAMForTest()
	a := getIPAM()
	root := "/var/lib/nyxd/ipam"
	fallback := filepath.Join(tmp, "nyxd-ipam")
	if !strings.HasPrefix(a.dir, root+string(os.PathSeparator)) && !strings.HasPrefix(a.dir, fallback+string(os.PathSeparator)) && a.dir != root && a.dir != fallback {
		t.Fatalf("ipam dir %s", a.dir)
	}
}

func TestNativeIPAMDirEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x", "ipam")
	t.Setenv("NYXD_IPAM_DIR", dir)
	resetIPAMForTest()
	if !strings.HasPrefix(getIPAM().dir, dir+string(os.PathSeparator)) {
		t.Fatal("NYXD_IPAM_DIR ignored")
	}
	resetIPAMForTest()
	t.Setenv("NYXD_IPAM_DIR", "/proc/nope/ipam")
	defer func() {
		if recover() == nil {
			t.Fatal("unusable NYXD_IPAM_DIR did not panic")
		}
		resetIPAMForTest()
	}()
	getIPAM()
}

func TestNetlinkHelpers(t *testing.T) {
	ack := func(errno int32, typ uint16, dataLen int) []byte {
		b := make([]byte, unix.NLMSG_HDRLEN+dataLen)
		binary.NativeEndian.PutUint32(b[0:], uint32(len(b)))
		binary.NativeEndian.PutUint16(b[4:], typ)
		if dataLen >= 4 {
			binary.NativeEndian.PutUint32(b[unix.NLMSG_HDRLEN:], uint32(errno))
		}
		return b
	}
	for name, c := range map[string]struct {
		b      []byte
		done   bool
		errSub string
	}{
		"ack":       {ack(0, unix.NLMSG_ERROR, 4), true, ""},
		"error":     {ack(-int32(unix.EEXIST), unix.NLMSG_ERROR, 4), true, "file exists"},
		"short ack": {ack(0, unix.NLMSG_ERROR, 0), true, "short ack"},
		"done":      {ack(0, unix.NLMSG_DONE, 4), true, ""},
		"other":     {ack(0, unix.RTM_NEWLINK, 4), false, ""},
		"truncated": {ack(0, unix.NLMSG_ERROR, 4)[:unix.NLMSG_HDRLEN+2], true, "nl parse"},
	} {
		if c.errSub == "nl parse" {
			binary.NativeEndian.PutUint32(c.b[0:], 99)
		}
		done, err := netlinkReply(c.b)
		if done != c.done || (c.errSub == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.errSub)) {
			t.Errorf("%s: done=%v err=%v", name, done, err)
		}
	}
	if _, err := parseNetlinkMessages(ack(0, unix.NLMSG_ERROR, 4)[:unix.NLMSG_HDRLEN]); err == nil {
		t.Error("header-only message with bad length accepted")
	}

	// Send/receive failures on a socket that is not netlink.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := netlinkDo(fds[0], newNetlinkMsg(unix.RTM_GETLINK, unix.NLM_F_REQUEST)); err == nil || !strings.Contains(err.Error(), "nl send") {
		t.Errorf("send on unix socket: %v", err)
	}
	unix.Close(fds[0])
	unix.Close(fds[1])

	fd, err := unixSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := linkSetUp(fd, "nope0"); err == nil {
		t.Error("linkSetUp unknown")
	}
	if err := addAddr(fd, "nope0", net.IPv4(10, 0, 0, 1), &net.IPNet{Mask: net.CIDRMask(8, 32)}); err == nil {
		t.Error("addAddr unknown")
	}
	if err := deleteLink("nope0"); err == nil {
		t.Error("deleteLink unknown")
	}
	if err := inNetNS("/nonexistent", func() error { return nil }); err == nil {
		t.Error("inNetNS bad path")
	}
	if err := inNetNS("/dev/null", func() error { return nil }); err == nil {
		t.Error("inNetNS on a non-namespace file")
	}
	if !isNotExist(unix.ENODEV) || !isNotExist(os.ErrNotExist) || isNotExist(unix.EPERM) {
		t.Error("isNotExist")
	}
	if h, ok := parseHandle("tcp dport 1 accept # handle 42"); !ok || h != 42 {
		t.Error("parseHandle")
	}
	if _, ok := parseHandle("# handle 99999999999999999999999"); ok {
		t.Error("parseHandle overflow")
	}
	if _, ok := parseHandle("no handle"); ok {
		t.Error("parseHandle without handle")
	}
	n := newNestedAttr(1)
	n.addAttrUint32(2, 7)
	n.addBytes([]byte{1})
	if len(n.serialize())%4 != 0 {
		t.Error("nested attr not aligned")
	}
}

func TestNativeFailures(t *testing.T) {
	if !inNS(t) {
		return
	}
	ctx := context.Background()
	// Read-only sysctls are only logged.
	if err := unix.Mount("tmpfs", "/proc/sys/net", "tmpfs", unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(quiet).EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Unmount("/proc/sys/net", 0); err != nil {
		t.Fatal(err)
	}

	// A full subnet: /30 has one address besides the gateway.
	t.Setenv("NYXD_CONTAINER_SUBNET", "10.88.0.0/30")
	t.Setenv("NYXD_IPAM_DIR", t.TempDir())
	resetIPAMForTest()
	if _, err := Setup(ctx, "one", newNetNS(t), nil, nil, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(ctx, "two", newNetNS(t), nil, nil, quiet); err == nil || !strings.Contains(err.Error(), "ipam") {
		t.Fatalf("full subnet: %v", err)
	}
	if err := Teardown(ctx, "one", quiet); err != nil {
		t.Fatal(err)
	}

	// The bridge disappears under a running daemon.
	if err := deleteLink(BridgeName); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(ctx, "three", newNetNS(t), nil, nil, quiet); err == nil || !strings.Contains(err.Error(), "attach bridge") {
		t.Fatalf("missing bridge: %v", err)
	}
	h, p := vethNames("four")
	if err := createVethPair(h, p, quiet); err != nil {
		t.Fatal(err)
	}
	if err := attachVethToBridge(h, "lo", quiet); err == nil || !strings.Contains(err.Error(), "set master") {
		t.Fatalf("attach to a non-bridge: %v", err)
	}

	// Teardown reports every failing step.
	fakeNft(t, "list chain ip nyxd-filter forward", "permission denied")
	if err := os.RemoveAll(os.Getenv("NYXD_IPAM_DIR")); err != nil {
		t.Fatal(err)
	}
	err := Teardown(ctx, "four", quiet)
	for _, want := range []string{"remove internal filter", "ipam release"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("teardown error %v lacks %q", err, want)
		}
	}
}

func TestNativeIPAMFallback(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	// /var/lib not writable: $TMPDIR/nyxd-ipam; neither writable: panic.
	if err := unix.Mount("tmpfs", "/var/lib", "tmpfs", unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("NYXD_IPAM_DIR", "")
	resetIPAMForTest()
	a := getIPAM()
	fallback := filepath.Join(tmp, "nyxd-ipam")
	if !strings.HasPrefix(a.dir, fallback+string(os.PathSeparator)) && a.dir != fallback {
		t.Fatalf("ipam dir %s", a.dir)
	}
	resetIPAMForTest()
	t.Setenv("TMPDIR", "/proc/nope")
	defer func() {
		if recover() == nil {
			t.Fatal("no usable ipam dir did not panic")
		}
		resetIPAMForTest()
	}()
	getIPAM()
}

func TestIPAMErrors(t *testing.T) {
	for name, args := range map[string][3]string{
		"cidr":            {t.TempDir(), "bogus", "10.0.0.1"},
		"ipv6":            {t.TempDir(), "fd00::/64", "fd00::1"},
		"gateway":         {t.TempDir(), "10.0.0.0/24", "nope"},
		"gateway outside": {t.TempDir(), "10.0.0.0/24", "10.0.1.1"},
		"gateway network": {t.TempDir(), "10.0.0.0/24", "10.0.0.0"},
		"dir":             {"/proc/nope/ipam", "10.0.0.0/24", "10.0.0.1"},
		"/31":             {t.TempDir(), "10.0.0.0/31", "10.0.0.1"},
	} {
		if _, err := newIPAM(args[0], args[1], args[2]); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}

	dir := t.TempDir()
	a, err := newIPAM(dir, "10.0.0.0/24", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "zz-not-a-lease"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.release("absent"); err != nil { // unreadable entries are skipped
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := a.allocate("c"); err == nil {
			t.Error("allocate in a read-only dir")
		}
		_ = os.Chmod(dir, 0o700)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.release("c"); err == nil {
		t.Error("release without a dir")
	}
	m := &nlMsg{}
	m.addBytes([]byte{1})
	if len(m.buf) != 4 {
		t.Error("addBytes padding")
	}
}
