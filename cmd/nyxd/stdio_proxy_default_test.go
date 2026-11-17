package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestProxyDefaultSocket(t *testing.T) {
	dir := t.TempDir()
	if got := proxyDefaultSocket(1000, dir); got != systemSocket {
		t.Fatalf("no rootless daemon: %s", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "nyxd"), 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "nyxd", "nyxd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := proxyDefaultSocket(1000, dir); got != sock {
		t.Fatalf("rootless daemon: %s", got)
	}
	if got := proxyDefaultSocket(0, dir); got != systemSocket {
		t.Fatalf("root uses the system daemon: %s", got)
	}
	if got := proxyDefaultSocket(1000, ""); got != systemSocket {
		t.Fatalf("no XDG_RUNTIME_DIR: %s", got)
	}
}
