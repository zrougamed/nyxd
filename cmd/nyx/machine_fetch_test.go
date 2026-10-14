package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchRootfsFailures(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("tar bytes")) //nolint:errcheck
	zw.Close()
	good := gz.Bytes()
	sum := sha256.Sum256(good)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintf(w, "%s  img\n", hex.EncodeToString(sum[:]))
		case r.URL.Path == "/drop.tgz":
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
		case r.URL.Path == "/short.tgz":
			w.Header().Set("Content-Length", "1000")
			w.Write(good[:10]) //nolint:errcheck
		default:
			w.Write(good) //nolint:errcheck
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	dst := filepath.Join(dir, "rootfs.tar")
	quiet := func(f func() error) error { _, _, err := capture(t, "", f); return err }

	if err := quiet(func() error { return fetchRootfs(srv.URL+"/img.tgz", dst) }); err != nil {
		t.Fatalf("download: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "tar bytes" {
		t.Errorf("rootfs %q", b)
	}
	for _, src := range []string{"/drop.tgz", "/short.tgz"} {
		if err := quiet(func() error { return fetchRootfs(srv.URL+src, dst) }); err == nil {
			t.Errorf("%s: no error", src)
		}
	}
	if err := quiet(func() error { return fetchRootfs(srv.URL+"/img.tgz", filepath.Join(dir, "no", "dst")) }); err == nil {
		t.Error("download into a missing directory")
	}
	// A local file: corrupt gzip data, and a destination that cannot be created.
	bad := filepath.Join(dir, "bad.tgz")
	os.WriteFile(bad, append(append([]byte{}, good[:12]...), bytes.Repeat([]byte{0xff}, 64)...), 0o600) //nolint:errcheck
	if err := fetchRootfs(bad, dst); err == nil {
		t.Error("corrupt gzip accepted")
	}
	if err := fetchRootfs(bad, filepath.Join(dir, "no", "dst")); err == nil {
		t.Error("copy into a missing directory")
	}
}

func TestMachineDispatchVfkit(t *testing.T) {
	t.Setenv("NYX_MACHINE_DRIVER", "vfkit")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := doMachine([]string{"run", "--name", "absent"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("run of a missing machine: %v", err)
	}
	if err := doMachine([]string{"bogus"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("unknown subcommand: %v", err)
	}
}
