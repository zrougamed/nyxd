package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineErrors(t *testing.T) {
	t.Setenv("NYX_WSL_EXE", "")
	if err := doMachine([]string{"status"}); err == nil || !strings.Contains(err.Error(), "on Linux run nyxd") {
		t.Errorf("machine on Linux: %v", err)
	}
	dir := t.TempDir()
	failing := filepath.Join(dir, "wsl.exe")
	_ = os.WriteFile(failing, []byte("#!/bin/sh\nprintf 'W\\000S\\000L\\000 broke' ; exit 1\n"), 0o755)
	t.Setenv("NYX_WSL_EXE", failing)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, args := range [][]string{{}, {"bogus"}, {"status", "extra"}, {"init", "--bogus"}} {
		if err := doMachine(args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	// init: development build without --image; import failure; bad tarball source.
	if err := doMachine([]string{"init"}); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("init without image: %v", err)
	}
	old := version
	version = "v9.9.9"
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if !strings.Contains(releaseBase, "github.com") {
		t.Fatal("release base")
	}
	if err := doMachine([]string{"init", "--image", srv.URL + "/x.tar.gz"}); err == nil {
		t.Error("init from a 404")
	}
	version = old
	tarball := filepath.Join(dir, "rootfs.tar")
	_ = os.WriteFile(tarball, []byte("tar"), 0o644)
	if err := doMachine([]string{"init", "--image", tarball}); err == nil || !strings.Contains(err.Error(), "WSL broke") {
		t.Errorf("import failure (UTF-16 output cleaned): %v", err)
	}
	if err := doMachine([]string{"init", "--image", filepath.Join(dir, "missing.tar")}); err == nil {
		t.Error("missing tarball")
	}
	// start: wsl.exe cannot be run; stop/rm/logs failures.
	t.Setenv("NYX_WSL_EXE", filepath.Join(dir, "nonexistent.exe"))
	if err := doMachine([]string{"start"}); err == nil {
		t.Error("start without wsl.exe")
	}
	t.Setenv("NYX_WSL_EXE", failing)
	if err := doMachine([]string{"stop"}); err == nil {
		t.Error("stop failure")
	}
	if err := doMachine([]string{"stop", "--terminate"}); err == nil {
		t.Error("terminate failure")
	}
	if err := doMachine([]string{"rm", "--name", "other"}); err == nil {
		t.Error("rm failure")
	}
	if err := doMachine([]string{"logs", "--name", "fresh"}); err == nil {
		t.Error("logs without a log file")
	}
	if err := doMachine([]string{"status"}); err != nil {
		t.Errorf("status of a stopped machine: %v", err)
	}
	// Cache dir unusable.
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, nil, 0o600)
	t.Setenv("XDG_CACHE_HOME", f)
	for _, sub := range []string{"init", "start", "logs"} {
		if err := doMachine([]string{sub, "--image", tarball}); err == nil && sub != "logs" {
			t.Errorf("%s with an unusable cache dir", sub)
		}
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := (machine{name: "x"}).dir(); err == nil {
		t.Error("no cache dir at all")
	}
	if nameFlag("nyx") != "" || nameFlag("b") != " --name b" {
		t.Error("nameFlag")
	}
}

func TestFetchRootfsErrors(t *testing.T) {
	dir := t.TempDir()
	if err := fetchRootfs(filepath.Join(dir, "missing"), filepath.Join(dir, "out")); err == nil {
		t.Error("missing source")
	}
	src := filepath.Join(dir, "src.tar")
	_ = os.WriteFile(src, []byte("x"), 0o644)
	if err := fetchRootfs(src, filepath.Join(dir, "no", "out")); err == nil {
		t.Error("destination in a missing dir")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.Write([]byte(strings.Repeat("a", 64))) //nolint:errcheck
			return
		}
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()
	if err := fetchRootfs(srv.URL+"/x.tar.gz", filepath.Join(dir, "o")); err == nil || !strings.Contains(err.Error(), "410") {
		t.Errorf("download 410: %v", err)
	}
	if err := fetchRootfs("http://127.0.0.1:1/x.tar.gz", filepath.Join(dir, "o")); err == nil {
		t.Error("unreachable")
	}
	if err := fetchRootfs(srv.URL+"/x.tar.gz", filepath.Join(dir, "no", "o")); err == nil {
		t.Error("download into a missing dir")
	}
}
