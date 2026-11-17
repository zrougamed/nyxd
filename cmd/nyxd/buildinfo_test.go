package main

import (
	"runtime/debug"
	"testing"

	"github.com/zrougamed/nyxd/internal/rootless"
)

func TestFillFromBuildInfo(t *testing.T) {
	vcs := func(kv ...string) *debug.BuildInfo {
		info := &debug.BuildInfo{}
		for i := 0; i < len(kv); i += 2 {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return info
	}
	const rev = "fd0a397c097a00335481a2265169f1871f934914"
	cases := []struct {
		name               string
		commit, date       string
		info               *debug.BuildInfo
		wantCommit, wantDt string
	}{
		{"no build info", "unknown", "unknown", nil, "unknown", "unknown"},
		{"no vcs stamp", "unknown", "unknown", vcs(), "unknown", "unknown"},
		{"vcs stamp fills both", "unknown", "unknown",
			vcs("vcs.revision", rev, "vcs.time", "2026-10-21T20:07:16Z", "vcs.modified", "false"),
			"fd0a397c", "2026-10-21T20:07:16Z"},
		{"dirty tree", "unknown", "unknown",
			vcs("vcs.revision", rev, "vcs.modified", "true"),
			"fd0a397c-dirty", "unknown"},
		{"ldflags win", "abc12345", "2026-11-17T00:00:00Z",
			vcs("vcs.revision", rev, "vcs.time", "2026-10-21T20:07:16Z"),
			"abc12345", "2026-11-17T00:00:00Z"},
		{"short revision kept", "unknown", "unknown", vcs("vcs.revision", "abc"), "abc", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotC, gotD := fillFromBuildInfo(c.commit, c.date, c.info)
			if gotC != c.wantCommit || gotD != c.wantDt {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotC, gotD, c.wantCommit, c.wantDt)
			}
		})
	}
}

func TestContainerLogDir(t *testing.T) {
	if got := containerLogDir(Config{BaseDir: "/var/lib/nyxd"}); got != "/var/lib/nyxd/logs" {
		t.Fatalf("default: %s", got)
	}
	if got := containerLogDir(Config{BaseDir: "/var/lib/nyxd", LogDir: "/run/nyxd/logs/"}); got != "/run/nyxd/logs" {
		t.Fatalf("-log-dir: %s", got)
	}
}

func TestDaemonInfo(t *testing.T) {
	if got := daemonInfo(rootless.Info{}, true, "/l", ""); got.Mode != "rootful" || got.HostUID != -1 || !got.Cgroups || got.LogDir != "/l" {
		t.Fatalf("rootful: %+v", got)
	}
	if got := daemonInfo(rootless.Info{Rootless: true, SubIDs: true}, false, "/l", "1000"); got.Mode != "rootless" || got.HostUID != 1000 || !got.SubIDs || got.Cgroups {
		t.Fatalf("rootless: %+v", got)
	}
	if got := daemonInfo(rootless.Info{Rootless: true}, false, "/l", "x"); got.HostUID != -1 {
		t.Fatalf("bad uid: %+v", got)
	}
	if got := daemonInfo(rootless.Info{Rootless: true, Remap: true, SubIDs: true}, true, "/l", "0"); got.Mode != "userns-remap" || got.HostUID != -1 {
		t.Fatalf("remap: %+v", got)
	}
}
