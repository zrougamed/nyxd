package network

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePlugins writes CNI plugins (shell scripts) that answer with an IP; a
// file "fail-<plugin>" in the directory makes that one fail (plugins get
// only the CNI environment, not ours).
func fakePlugins(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"bridge", "portmap", "firewall", "tuning"} {
		script := `#!/bin/sh
cat >/dev/null
[ -e "` + dir + `/fail-` + p + `" ] && { echo '{"code":7,"msg":"` + p + ` failed"}'; echo oops >&2; exit 1; }
[ "$CNI_COMMAND" = DEL ] && exit 0
echo '{"cniVersion":"1.0.0","ips":[{"address":"10.88.0.7/16"}]}'
`
		if err := os.WriteFile(filepath.Join(dir, p), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCNIManager(t *testing.T) {
	ctx := context.Background()
	if m := NewManager("", "", ""); m.confDir != cniConfDir || m.binDir != cniPluginDir || m.netName != "nyx" {
		t.Errorf("defaults: %+v", m)
	}
	conf := filepath.Join(t.TempDir(), "net.d")
	m := NewManager("test", conf, fakePlugins(t))
	if _, err := m.Setup(ctx, "c", "/ns", nil, nil); err == nil {
		t.Error("setup before EnsureNetwork")
	}
	if err := m.Teardown(ctx, "c", "/ns"); err == nil {
		t.Error("teardown before EnsureNetwork")
	}
	if err := m.EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureNetwork(); err != nil { // existing config kept
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(conf, "10-nyx.conflist"))
	if !strings.Contains(string(b), `"10.88.0.1"`) || !strings.Contains(string(b), `"name": "test"`) {
		t.Errorf("conflist:\n%s", b)
	}
	ip, err := m.Setup(ctx, "c", "/ns", []PortMapping{{HostPort: 1, ContainerPort: 2}}, &SetupOptions{Internal: true})
	if err != nil || ip != "10.88.0.7" {
		t.Fatalf("setup: %q %v", ip, err)
	}
	if err := m.Teardown(ctx, "c", "/ns"); err != nil {
		t.Fatal(err)
	}
	failMark := filepath.Join(m.binDir, "fail-portmap")
	_ = os.WriteFile(failMark, nil, 0o600)
	if _, err := m.Setup(ctx, "c", "/ns", nil, nil); err == nil || !strings.Contains(err.Error(), "portmap failed") || !strings.Contains(err.Error(), "oops") {
		t.Errorf("failing plugin: %v", err)
	}
	_ = os.Remove(failMark)
	_ = os.Remove(filepath.Join(m.binDir, "tuning"))
	if _, err := m.Setup(ctx, "c", "/ns", nil, nil); err == nil || !strings.Contains(err.Error(), "plugin not found") {
		t.Errorf("missing plugin: %v", err)
	}
	for _, bad := range []string{"{", `{"plugins":[1]}`} {
		if err := os.WriteFile(filepath.Join(conf, "10-nyx.conflist"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Setup(ctx, "c", "/ns", nil, nil); err == nil {
			t.Errorf("conflist %q accepted", bad)
		}
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	if err := NewManager("x", filepath.Join(f, "conf"), "/bin").EnsureNetwork(); err == nil {
		t.Error("conf dir under a file")
	}
}

func TestCNIHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "{": "", `{"ips":[]}`: "", `{"ips":[{"address":"10.0.0.2/24"}]}`: "10.0.0.2", `{"ips":[{"address":"fd00::1"}]}`: "fd00::1",
	} {
		var b []byte
		if in != "" {
			b = []byte(in)
		}
		if got := extractIP(b); got != want {
			t.Errorf("extractIP(%q) = %q", in, got)
		}
	}
	if firstIP("bogus") != "10.88.0.1" || firstIP("192.168.5.0/24") != "192.168.5.1" {
		t.Error("firstIP")
	}
}

func TestParseDockerPublishForms(t *testing.T) {
	for in, want := range map[string]PortMapping{
		"80":                {80, 80, "tcp"},
		"8080:80":           {8080, 80, "tcp"},
		"53:53/UDP":         {53, 53, "udp"},
		"127.0.0.1:8080:80": {8080, 80, "tcp"},
		" 1:2 /tcp":         {1, 2, "tcp"},
	} {
		got, err := ParseDockerPublish(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "0", "70000", "1:x", "1:2/sctp", "a:x:2", "1:2:3:4", "1:0"} {
		if _, err := ParseDockerPublish(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if (HostBackend{}).EnsureNetwork() != nil || (HostBackend{}).Teardown(context.Background(), "", "") != nil {
		t.Error("host backend")
	}
	if ip, err := (HostBackend{}).Setup(context.Background(), "", "", nil, nil); ip != "" || err != nil {
		t.Error("host setup")
	}
}
