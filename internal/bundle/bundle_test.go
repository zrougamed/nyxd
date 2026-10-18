package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestGenerateSeccompDefault(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "b1")
	_, err := Generate(bundleDir, Options{
		ContainerID: "c1",
		RootFS:      "/tmp/rootfs",
		ImageConfig: &oci.ImageConfig{Config: oci.ContainerConfig{Cmd: []string{"sleep", "1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(bundleDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	linux := raw["linux"].(map[string]any)
	sc := linux["seccomp"].(map[string]any)
	if sc["defaultAction"] != "SCMP_ACT_ALLOW" {
		t.Fatalf("seccomp default: %#v", sc["defaultAction"])
	}
}

func TestGeneratePrivilegedNoSeccomp(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "b2")
	_, err := Generate(bundleDir, Options{
		ContainerID: "c2",
		RootFS:      "/tmp/rootfs",
		Privileged:  true,
		ImageConfig: &oci.ImageConfig{Config: oci.ContainerConfig{Cmd: []string{"sleep", "1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(bundleDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	linux := raw["linux"].(map[string]any)
	if _, ok := linux["seccomp"]; ok {
		t.Fatal("expected no seccomp for privileged")
	}
	proc := raw["process"].(map[string]any)
	if proc["noNewPrivileges"].(bool) {
		t.Fatal("privileged should disable noNewPrivileges")
	}
}

func TestBuildSpecHostNetworkRootless(t *testing.T) {
	img := &oci.ImageConfig{Config: oci.ContainerConfig{Cmd: []string{"true"}}}
	cases := []struct {
		name               string
		opts               Options
		wantNetNS, sysBind bool
	}{
		{"default", Options{ImageConfig: img}, true, false},
		{"host rootful", Options{ImageConfig: img, HostNetwork: true}, false, false},
		{"host rootless", Options{ImageConfig: img, HostNetwork: true, Rootless: true}, false, true},
		{"netns rootless", Options{ImageConfig: img, NetNS: "/run/x", Rootless: true}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := buildSpec(c.opts, nil)
			hasNet := false
			for _, ns := range spec.Linux.Namespaces {
				if ns.Type == "network" {
					hasNet = true
				}
			}
			if hasNet != c.wantNetNS {
				t.Fatalf("network namespace = %v, want %v", hasNet, c.wantNetNS)
			}
			var sys *Mount
			for i, m := range spec.Mounts {
				if m.Destination == "/sys/fs/cgroup" && c.sysBind {
					t.Fatal("/sys/fs/cgroup mount kept alongside /sys rbind")
				}
				if m.Destination == "/sys" {
					sys = &spec.Mounts[i]
				}
			}
			if sys == nil {
				t.Fatal("no /sys mount")
			}
			if (sys.Type == "bind") != c.sysBind {
				t.Fatalf("/sys type = %q, want bind=%v", sys.Type, c.sysBind)
			}
		})
	}
}

func TestGenerateApparmorAndUlimits(t *testing.T) {
	dir := t.TempDir()
	_, err := Generate(dir, Options{
		ContainerID:     "c",
		RootFS:          dir,
		Args:            []string{"/bin/true"},
		ApparmorProfile: "gh-buildkit",
		Rlimits: []Rlimit{
			{Type: "RLIMIT_NOFILE", Soft: 65536, Hard: 65536},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Process.ApparmorProfile != "gh-buildkit" {
		t.Fatalf("apparmor=%q", spec.Process.ApparmorProfile)
	}
	if len(spec.Process.Rlimits) != 1 || spec.Process.Rlimits[0].Soft != 65536 {
		t.Fatalf("rlimits=%+v", spec.Process.Rlimits)
	}
}

func TestAppendMountsResolvWins(t *testing.T) {
	base := resolvThenDefault("/host/resolv", false)
	extra := []Mount{{
		Type: "bind", Source: "/caller/resolv", Destination: "/etc/resolv.conf",
		Options: []string{"rbind", "ro"},
	}}
	got := appendMounts(base, extra)
	var resolv int
	var src string
	for _, m := range got {
		if filepath.Clean(m.Destination) == "/etc/resolv.conf" {
			resolv++
			src = m.Source
		}
	}
	if resolv != 1 || src != "/caller/resolv" {
		t.Fatalf("resolv mounts=%d src=%q", resolv, src)
	}
}

func TestWithSwapLimit(t *testing.T) {
	if got := withSwapLimit(nil); got != nil {
		t.Fatalf("nil resources: got %+v", got)
	}
	in := &Resources{Memory: &MemoryRes{Limit: 32 << 20}}
	got := withSwapLimit(in)
	if got.Memory.Swap != 64<<20 {
		t.Fatalf("swap = %d, want %d", got.Memory.Swap, 64<<20)
	}
	if in.Memory.Swap != 0 {
		t.Fatal("caller's resources were changed")
	}
	set := &Resources{Memory: &MemoryRes{Limit: 32 << 20, Swap: -1}}
	if withSwapLimit(set).Memory.Swap != -1 {
		t.Fatal("explicit swap value overridden")
	}
	noLimit := &Resources{Memory: &MemoryRes{Reservation: 16 << 20}}
	if withSwapLimit(noLimit).Memory.Swap != 0 {
		t.Fatal("swap set without a memory limit")
	}
}
