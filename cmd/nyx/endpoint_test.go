package main

import (
	"strings"
	"testing"
)

func TestDaemonPath(t *testing.T) {
	if p, _ := daemonPath("/run/nyxd/nyxd.sock", `C:\x`); p != `C:\x` {
		t.Errorf("unix endpoint must not translate: %q", p)
	}
	if p, _ := daemonPath("wsl://nyx", `C:\x`); p != "/mnt/c/x" {
		t.Errorf("wsl endpoint translate: %q", p)
	}
	if !isWSLEndpoint("wsl://nyx") || isWSLEndpoint("unix:///x") || !strings.HasPrefix(wslScheme+"nyx", "wsl://") {
		t.Error("endpoint detection")
	}
}
