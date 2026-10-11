package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCapabilityMerge(t *testing.T) {
	for in, want := range map[string]string{"": "", " net_admin ": "CAP_NET_ADMIN", "CAP_CHOWN": "CAP_CHOWN", "all": "ALL"} {
		if got := normalizeCap(in); got != want {
			t.Errorf("normalizeCap(%q) = %q", in, got)
		}
	}
	base := []string{"CAP_CHOWN", "CAP_KILL"}
	c := mergeCapabilities(base, []string{"net_admin", "", "ALL"}, []string{"kill", ""})
	if !slices.Equal(c.Bounding, []string{"CAP_CHOWN", "CAP_NET_ADMIN"}) {
		t.Errorf("add/drop: %v", c.Bounding)
	}
	c = mergeCapabilities(base, []string{"SYS_TIME"}, []string{"ALL"})
	if !slices.Equal(c.Effective, []string{"CAP_SYS_TIME"}) {
		t.Errorf("drop ALL then add: %v", c.Effective)
	}
	if len(allCaps().Bounding) < 30 {
		t.Error("allCaps")
	}
}

func TestSeccompProfiles(t *testing.T) {
	for arch, n := range map[string]int{"amd64": 3, "arm64": 2, "arm": 1, "386": 1, "riscv64": 1, "mips": 0} {
		if got := seccompArchitectures(arch); len(got) != n {
			t.Errorf("%s: %v", arch, got)
		}
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(good, []byte(`{"defaultAction":"SCMP_ACT_ERRNO"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "noaction.json"), []byte(`{}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "junk.json"), []byte(`{`), 0o644)
	if sc, err := resolveSeccomp(Options{SeccompProfile: good}); err != nil || sc.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Errorf("custom profile: %+v %v", sc, err)
	}
	for _, p := range []string{"noaction.json", "junk.json", "missing.json"} {
		if _, err := resolveSeccomp(Options{SeccompProfile: filepath.Join(dir, p)}); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	for _, p := range []string{"unconfined", "disabled"} {
		if sc, err := resolveSeccomp(Options{SeccompProfile: p}); sc != nil || err != nil {
			t.Errorf("%s: %v %v", p, sc, err)
		}
	}
	for _, p := range []string{"", "default", "builtin", "runtime/default"} {
		if sc, _ := resolveSeccomp(Options{SeccompProfile: p}); sc == nil {
			t.Errorf("%q: no default profile", p)
		}
	}
}

func TestGenerateErrorsAndUserns(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "file"), nil, 0o600)
	if _, err := Generate(filepath.Join(dir, "file", "b"), Options{}); err == nil {
		t.Error("bundle dir under a file")
	}
	if _, err := Generate(filepath.Join(dir, "b1"), Options{SeccompProfile: "/missing.json"}); err == nil {
		t.Error("missing seccomp profile")
	}
	b := filepath.Join(dir, "b2")
	_ = os.MkdirAll(filepath.Join(b, "config.json"), 0o700)
	if _, err := Generate(b, Options{}); err == nil {
		t.Error("config.json is a directory")
	}
	b = filepath.Join(dir, "b3")
	if _, err := Generate(b, Options{ContainerID: "c", RootFS: "/r",
		UIDMappings: []LinuxIDMapping{{ContainerID: 0, HostID: 1000, Size: 1}},
		GIDMappings: []LinuxIDMapping{{ContainerID: 0, HostID: 1000, Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(b, "config.json"))
	var spec map[string]any
	_ = json.Unmarshal(data, &spec)
	if !strings.Contains(string(data), `"type": "user"`) {
		t.Error("ID mappings without a user namespace")
	}
}
