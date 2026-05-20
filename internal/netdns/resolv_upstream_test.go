package netdns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseResolvConfNameservers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "resolv.conf")
	content := "nameserver 127.0.0.53\n# foo\nnameserver 2001:db8::1\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := parseResolvConfNameservers(p)
	if len(got) != 2 {
		t.Fatalf("got %d servers: %v", len(got), got)
	}
	if got[0] != "127.0.0.53:53" {
		t.Errorf("first: %q", got[0])
	}
	if got[1] != "[2001:db8::1]:53" {
		t.Errorf("second: %q", got[1])
	}
}
