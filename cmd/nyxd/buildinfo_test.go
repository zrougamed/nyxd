package main

import (
	"runtime/debug"
	"testing"
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
