package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
	"gopkg.in/yaml.v3"
)

func TestBundleResourcesMemorySwap(t *testing.T) {
	d := &Deploy{Resources: Resources{Limits: ResourceSpec{Memory: "4g", MemorySwap: "4g"}}}
	r, err := BundleResourcesFromDeploy(d)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Memory == nil {
		t.Fatal("nil memory")
	}
	const fourG = int64(4 * 1024 * 1024 * 1024)
	if r.Memory.Limit != fourG || r.Memory.Swap != fourG {
		t.Fatalf("limit=%d swap=%d", r.Memory.Limit, r.Memory.Swap)
	}
	// Explicit Swap equal to Limit: bundle.withSwapLimit must not double it.
	dir := t.TempDir()
	if _, err := bundle.Generate(dir, bundle.Options{
		ContainerID: "c",
		RootFS:      dir,
		Resources:   r,
		Args:        []string{"/bin/true"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec bundle.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Linux == nil || spec.Linux.Resources == nil || spec.Linux.Resources.Memory == nil {
		t.Fatal("missing memory resources")
	}
	if spec.Linux.Resources.Memory.Swap != fourG {
		t.Fatalf("oci swap=%d want %d", spec.Linux.Resources.Memory.Swap, fourG)
	}
}

func TestBundleRlimitsFromUlimits(t *testing.T) {
	got, err := BundleRlimitsFromUlimits(map[string]Ulimit{
		"nofile": {Soft: 65536, Hard: 65536},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "RLIMIT_NOFILE" || got[0].Soft != 65536 || got[0].Hard != 65536 {
		t.Fatalf("%+v", got)
	}
	if _, err := BundleRlimitsFromUlimits(map[string]Ulimit{"wat": {Soft: 1, Hard: 1}}); err == nil {
		t.Fatal("expected unsupported ulimit error")
	}
}

func TestUlimitYAMLScalar(t *testing.T) {
	var u Ulimit
	if err := yaml.Unmarshal([]byte("65536"), &u); err != nil {
		t.Fatal(err)
	}
	if u.Soft != 65536 || u.Hard != 65536 {
		t.Fatalf("%+v", u)
	}
}

func TestResolveServiceBridge(t *testing.T) {
	stack := &Stack{
		Networks: map[string]Network{
			"runner": {Driver: "bridge", Bridge: "nyxbr-runner", Subnet: "10.89.0.0/16"},
			"other":  {Driver: "bridge", Subnet: "10.90.0.0/16"},
		},
	}
	got, err := ResolveServiceBridge(stack, Service{Networks: []string{"runner"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bridge != "nyxbr-runner" || got.Subnet != "10.89.0.0/16" || got.Gateway != "10.89.0.1" {
		t.Fatalf("%+v", got)
	}
	// Omitting networks stays on the default bridge.
	got, err = ResolveServiceBridge(stack, Service{})
	if err != nil || got.Bridge != "" {
		t.Fatalf("omit networks: %+v %v", got, err)
	}
	// Network without bridge name is ignored.
	got, err = ResolveServiceBridge(stack, Service{Networks: []string{"other"}})
	if err != nil || got.Bridge != "" {
		t.Fatalf("no bridge field: %+v %v", got, err)
	}
}

func TestValidateDNSServers(t *testing.T) {
	got, err := validateDNSServers([]string{"1.1.1.1", "8.8.8.8"})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := validateDNSServers([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error")
	}
}
