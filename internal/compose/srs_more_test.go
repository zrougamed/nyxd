package compose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUlimitYAMLMapAndRlimits(t *testing.T) {
	var u Ulimit
	if err := yaml.Unmarshal([]byte("soft: 1\nhard: 2\n"), &u); err != nil {
		t.Fatal(err)
	}
	if u.Soft != 1 || u.Hard != 2 {
		t.Fatalf("%+v", u)
	}
	got, err := BundleRlimitsFromUlimits(map[string]Ulimit{
		"nofile":        {Soft: 10, Hard: 0},
		"nproc":         {Soft: 0, Hard: 20},
		"core":          {Soft: 1, Hard: 1},
		"memlock":       {Soft: 1, Hard: 1},
		"stack":         {Soft: 1, Hard: 1},
		"as":            {Soft: 1, Hard: 1},
		"RLIMIT_NOFILE": {Soft: 3, Hard: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 6 {
		t.Fatalf("got %d rlimits", len(got))
	}
	if _, err := BundleRlimitsFromUlimits(map[string]Ulimit{"nofile": {}}); err == nil {
		t.Fatal("zero soft/hard")
	}
	if _, err := BundleRlimitsFromUlimits(map[string]Ulimit{"nofile": {Soft: 9, Hard: 3}}); err == nil {
		t.Fatal("soft > hard")
	}
}

func TestResolveServiceBridgeErrors(t *testing.T) {
	_, err := ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"bad": {Bridge: "this-name-is-way-too-long", Subnet: "10.1.0.0/16"},
	}}, Service{Networks: []string{"bad"}})
	if err == nil || !strings.Contains(err.Error(), "15") {
		t.Fatalf("long name: %v", err)
	}
	_, err = ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"bad": {Bridge: "nyxbr!", Subnet: "10.1.0.0/16"},
	}}, Service{Networks: []string{"bad"}})
	if err == nil {
		t.Fatal("bad char")
	}
	_, err = ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"bad": {Bridge: "docker0", Subnet: "172.17.0.0/16"},
	}}, Service{Networks: []string{"bad"}})
	if err == nil || !strings.Contains(err.Error(), "nyxbr") {
		t.Fatalf("foreign bridge: %v", err)
	}
	_, err = ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"bad": {Bridge: "nyxbr1", Subnet: "not-cidr"},
	}}, Service{Networks: []string{"bad"}})
	if err == nil {
		t.Fatal("bad subnet")
	}
	_, err = ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"bad": {Bridge: "nyxbr1", Subnet: "fd00::/64"},
	}}, Service{Networks: []string{"bad"}})
	if err == nil {
		t.Fatal("ipv6")
	}
	_, err = ResolveServiceBridge(nil, Service{Networks: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	// Unknown network name / incomplete bridge+subnet → default bridge.
	got, err := ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"plain": {Driver: "bridge"},
		"half":  {Bridge: "nyxbr9"},
	}}, Service{Networks: []string{"missing", "plain", "half"}})
	if err != nil || got.Bridge != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := validateBridgeName(""); err == nil {
		t.Fatal("empty bridge")
	}
	if _, err := gatewayFromSubnet("10.1.0.0/32"); err == nil {
		t.Fatal("/32 has no usable gateway")
	}
	if _, err := gatewayFromSubnet("10.1.0.0/31"); err == nil {
		t.Fatal("/31 has no usable gateway")
	}
	got, err = ResolveServiceBridge(&Stack{Networks: map[string]Network{
		"ok": {Bridge: "nyxbr_ok", Subnet: "10.91.0.0/16"},
	}}, Service{Networks: []string{"ok"}})
	if err != nil || got.Bridge != "nyxbr_ok" || got.Gateway != "10.91.0.1" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestMemorySwapParseError(t *testing.T) {
	_, err := BundleResourcesFromDeploy(&Deploy{Resources: Resources{Limits: ResourceSpec{MemorySwap: "nope"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDNSServersEmptyEntries(t *testing.T) {
	got, err := validateDNSServers([]string{"", " 1.1.1.1 "})
	if err != nil || len(got) != 1 || got[0] != "1.1.1.1" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestDurationUnmarshal(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte(`"5s"`), &d); err != nil || d.Duration.Seconds() != 5 {
		t.Fatalf("%v %v", d, err)
	}
	if err := yaml.Unmarshal([]byte(`"nope"`), &d); err == nil {
		t.Fatal("bad duration")
	}
}
