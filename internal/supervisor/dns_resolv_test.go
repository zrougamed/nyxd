package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
)

func TestWriteBundleResolvCallerDNS(t *testing.T) {
	dir := t.TempDir()
	s := &Supervisor{netDriver: "native", dnsMode: "auto", dnsGateway: "10.88.0.1"}
	p, err := s.writeBundleResolv(dir, ContainerSpec{DNS: []string{"1.1.1.1", "8.8.8.8"}})
	if err != nil || p == "" {
		t.Fatalf("%q %v", p, err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "nameserver 1.1.1.1") || !strings.Contains(body, "nameserver 8.8.8.8") {
		t.Fatalf("body=%q", body)
	}
	if strings.Contains(body, "10.88.0.1") {
		t.Fatal("gateway nameserver also written")
	}
}

func TestWriteBundleResolvVolumeWins(t *testing.T) {
	dir := t.TempDir()
	s := &Supervisor{netDriver: "native", dnsMode: "auto", dnsGateway: "10.88.0.1"}
	p, err := s.writeBundleResolv(dir, ContainerSpec{
		ExtraMounts: []bundle.Mount{{
			Type: "bind", Source: "/tmp/x", Destination: "/etc/resolv.conf",
		}},
		DNS: []string{"1.1.1.1"},
	})
	if err != nil || p != "" {
		t.Fatalf("want empty path, got %q %v", p, err)
	}
}

func TestWriteBundleResolvCustomGateway(t *testing.T) {
	dir := t.TempDir()
	s := &Supervisor{netDriver: "native", dnsMode: "auto", dnsGateway: "10.88.0.1"}
	p, err := s.writeBundleResolv(dir, ContainerSpec{NetworkGateway: "10.89.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "nameserver 10.89.0.1") {
		t.Fatalf("%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "resolv.conf.host")); err != nil {
		t.Fatal(err)
	}
}
