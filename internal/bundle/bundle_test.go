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
