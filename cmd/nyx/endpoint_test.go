package main

import (
	"strings"
	"testing"
)

func TestToWSLPath(t *testing.T) {
	ok := map[string]string{
		`C:\Users\me\app\compose.yaml`: "/mnt/c/Users/me/app/compose.yaml",
		`d:\data`:                      "/mnt/d/data",
		`E:/mixed/slashes`:             "/mnt/e/mixed/slashes",
		`C:\`:                          "/mnt/c",
		`/already/linux`:               "/already/linux",
	}
	for in, want := range ok {
		got, err := toWSLPath(in)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`\\server\share\x`, `//server/share`, `relative\path`, `1:\x`, ``} {
		if _, err := toWSLPath(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
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
