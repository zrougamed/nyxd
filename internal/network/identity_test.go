package network

import (
	"fmt"
	"net"
	"runtime"
	"testing"
)

func TestActiveIdentityNativeDefaults(t *testing.T) {
	t.Setenv("NYXD_GATEWAY_IP", "")
	t.Setenv("NYXD_CONTAINER_SUBNET", "")
	id := ActiveIdentity("native")
	if id.Driver != "native" || id.Bridge != "nyxbr0" {
		t.Fatalf("%+v", id)
	}
	if id.Gateway == "" || id.Subnet == "" {
		t.Fatalf("%+v", id)
	}
}

func TestActiveIdentityEnvOverrides(t *testing.T) {
	t.Setenv("NYXD_GATEWAY_IP", "10.9.0.1")
	t.Setenv("NYXD_CONTAINER_SUBNET", "10.9.0.0/16")
	old := liveBridgeLookup
	liveBridgeLookup = func(string) (string, string, error) {
		return "", "", fmt.Errorf("no bridge")
	}
	t.Cleanup(func() { liveBridgeLookup = old })
	id := ActiveIdentity("native")
	if id.Bridge != "nyxbr0" || id.Gateway != "10.9.0.1" || id.Subnet != "10.9.0.0/16" {
		t.Fatalf("%+v", id)
	}
}

func TestActiveIdentityLiveBridgeWins(t *testing.T) {
	t.Setenv("NYXD_GATEWAY_IP", "10.9.0.1")
	t.Setenv("NYXD_CONTAINER_SUBNET", "10.9.0.0/16")
	old := liveBridgeLookup
	liveBridgeLookup = func(iface string) (string, string, error) {
		if iface != "nyxbr0" {
			t.Fatalf("iface=%q", iface)
		}
		return "10.88.0.1", "10.88.0.0/16", nil
	}
	t.Cleanup(func() { liveBridgeLookup = old })
	id := ActiveIdentity("native")
	if id.Gateway != "10.88.0.1" || id.Subnet != "10.88.0.0/16" {
		t.Fatalf("%+v", id)
	}
	liveBridgeLookup = func(string) (string, string, error) {
		return "10.88.0.1", "", nil
	}
	id = ActiveIdentity("native")
	if id.Gateway != "10.88.0.1" || id.Subnet != "10.9.0.0/16" {
		t.Fatalf("empty live subnet should keep env: %+v", id)
	}
}

func TestActiveIdentityHostAndCNI(t *testing.T) {
	if id := ActiveIdentity("host"); id.Driver != "host" || id.Bridge != "" {
		t.Fatalf("%+v", id)
	}
	if id := ActiveIdentity("cni"); id.Driver != "cni" || id.Bridge != "" {
		t.Fatalf("%+v", id)
	}
	if id := ActiveIdentity(""); id.Driver != "native" {
		t.Fatalf("%+v", id)
	}
}

func TestLiveBridgeIPv4Loopback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no lo")
	}
	gw, subnet, err := liveBridgeIPv4("lo")
	if err != nil {
		// Some environments have no IPv4 on lo; still exercise the error path.
		t.Log(err)
		return
	}
	if gw == "" || subnet == "" {
		t.Fatalf("gw=%q subnet=%q", gw, subnet)
	}
	_, _, err = liveBridgeIPv4("nyxd-no-such-iface")
	if err == nil {
		t.Fatal("expected missing iface error")
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("NYXD_TEST_ENVOR", "x")
	if envOr("NYXD_TEST_ENVOR", "d") != "x" {
		t.Fatal("env")
	}
	t.Setenv("NYXD_TEST_ENVOR", "")
	if envOr("NYXD_TEST_ENVOR", "d") != "d" {
		t.Fatal("default")
	}
}

type notIPNet struct{}

func (notIPNet) Network() string { return "ip+net" }
func (notIPNet) String() string  { return "x" }

func TestFirstIPv4CIDR(t *testing.T) {
	_, _, err := firstIPv4CIDR(nil)
	if err == nil {
		t.Fatal("empty")
	}
	_, ipnet, err := net.ParseCIDR("fd00::1/64")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = firstIPv4CIDR([]net.Addr{notIPNet{}, ipnet})
	if err == nil {
		t.Fatal("ipv6 only")
	}
	ip, v4, err := net.ParseCIDR("10.1.2.3/16")
	if err != nil {
		t.Fatal(err)
	}
	v4.IP = ip
	gw, subnet, err := firstIPv4CIDR([]net.Addr{notIPNet{}, ipnet, v4})
	if err != nil || gw != "10.1.2.3" || subnet != "10.1.0.0/16" {
		t.Fatalf("%q %q %v", gw, subnet, err)
	}
}
