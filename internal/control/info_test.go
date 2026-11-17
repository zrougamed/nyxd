package control

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

func TestHandleInfo(t *testing.T) {
	dir := t.TempDir()
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "v1.3.0", "abc12345", "2026-11-17", dir, "/run/nyxd/nyxd.sock", "", "host", "auto")
	s.SetLogDir("/run/nyxd/logs")
	s.SetDaemonInfo(DaemonInfo{Mode: "rootless", HostUID: 1000, SubIDs: true, Cgroups: false, LogDir: "/run/nyxd/logs"})
	rec := httptest.NewRecorder()
	s.handleInfo(rec, httptest.NewRequest("GET", "/v1/info", nil))
	var got InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	if got.ServerVersion != "v1.3.0" || got.Commit != "abc12345" || got.Mode != "rootless" ||
		got.HostUID == nil || *got.HostUID != 1000 || !got.SubIDs || got.Limits || !got.UserXattr ||
		got.BaseDir != dir || got.LogDir != "/run/nyxd/logs" || got.NetworkDriver != "host" || got.OS == "" {
		t.Fatalf("info: %+v", got)
	}

	// Without SetDaemonInfo (tests, embedders) it reports a rootful daemon.
	s2 := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "v1", "c", "d", dir, "", "", "native", "auto")
	rec = httptest.NewRecorder()
	s2.handleInfo(rec, httptest.NewRequest("GET", "/v1/info", nil))
	got = InfoResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Mode != "rootful" || got.HostUID != nil || got.UserXattr {
		t.Fatalf("default: %+v", got)
	}
}

func TestParseCrunVersion(t *testing.T) {
	for in, want := range map[string]string{
		"crun version 1.21\ncommit: abc\nspec: 1.0.0\n": "1.21",
		"crun version 1.14.1":                           "1.14.1",
		"something else\n":                              "something else",
		"":                                              "",
	} {
		if got := parseCrunVersion(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if readTrimmed("/nonexistent") != "" || fileExists("/nonexistent") || freeBytes("") != 0 {
		t.Fatal("missing files")
	}
}
