package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBundleHosts(t *testing.T) {
	host := filepath.Join(t.TempDir(), "hosts")
	os.WriteFile(host, []byte("127.0.0.1 localhost\n10.0.0.5 nas"), 0o644) //nolint:errcheck
	old := hostsFile
	hostsFile = host
	t.Cleanup(func() { hostsFile = old })

	dir := t.TempDir()
	s := &Supervisor{netDriver: "host"}
	p, err := s.writeBundleHosts(dir, ContainerSpec{Hostname: "Web", HostAliases: []string{"db", "web", "", "bad name", "webapp-db"}})
	if err != nil || p != filepath.Join(dir, "hosts") {
		t.Fatalf("write: %q %v", p, err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if !strings.Contains(got, "10.0.0.5 nas\n") || !strings.HasSuffix(got, "127.0.0.1\tweb db webapp-db\n") {
		t.Errorf("hosts:\n%s", got)
	}

	hostsFile = filepath.Join(t.TempDir(), "missing")
	p, _ = s.writeBundleHosts(dir, ContainerSpec{})
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "localhost") || strings.Contains(string(b), "nyxd:") {
		t.Errorf("fallback hosts:\n%s", b)
	}
	if p, err := (&Supervisor{netDriver: "native"}).writeBundleHosts(dir, ContainerSpec{Hostname: "x"}); p != "" || err != nil {
		t.Errorf("native driver wrote hosts: %q %v", p, err)
	}
}
