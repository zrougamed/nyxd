package control

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
)

func TestLocateCopyPath(t *testing.T) {
	vol := t.TempDir()
	file := filepath.Join(t.TempDir(), "app.conf")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mounts := []bundle.Mount{
		{Destination: "/data", Type: "bind", Source: vol, Options: []string{"rbind"}},
		{Destination: "/data/cache", Type: "tmpfs"},
		{Destination: "/etc/app.conf", Source: file, Options: []string{"bind", "ro"}},
	}
	for p, want := range map[string]copyLocation{
		"/etc/hosts":      {base: "/rootfs", rel: "etc/hosts"},
		"/":               {base: "/rootfs", rel: ""},
		"/data":           {base: vol, rel: ""},
		"/data/x/../y":    {base: vol, rel: "y"},
		"/database":       {base: "/rootfs", rel: "database"},
		"/etc/app.conf":   {base: filepath.Dir(file), rel: "app.conf"},
		"/var/../etc/foo": {base: "/rootfs", rel: "etc/foo"},
	} {
		got, err := locateCopyPath("/rootfs", mounts, p)
		if err != nil || got != want {
			t.Errorf("%s: got %+v %v, want %+v", p, got, err, want)
		}
	}
	for _, bad := range []string{"relative/path", "/proc", "/proc/1/environ", "/sys/kernel", "/dev/sda", "/data/cache/x"} {
		if _, err := locateCopyPath("/rootfs", mounts, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestResolveInRoot(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"usr/lib", "run", "etc"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"lib":       "usr/lib",       // relative
		"var/run":   "/run",          // absolute: from the container root
		"tmp/root":  "/",             // the container's /, not the host's
		"tmp/up":    "../../../../x", // ".." stops at the root
		"loop/a":    "b",
		"loop/b":    "a",
		"etc/mtab2": "/proc/mounts",
	}
	for l, target := range links {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(l)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, l)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, c := range []struct {
		rel        string
		followLast bool
		want       string
	}{
		{"lib/x.so", false, "usr/lib/x.so"},
		{"var/run/nyx", false, "run/nyx"},
		{"var/run", false, "var/run"}, // the link itself
		{"var/run", true, "run"},
		{"tmp/root/etc/new", false, "etc/new"},
		{"tmp/up", true, "x"},
		{"", false, "."},
		{"tmp/root", true, "."},
	} {
		got, err := resolveInRoot(root, c.rel, c.followLast)
		if err != nil || got != c.want {
			t.Errorf("%q follow=%v: got %q %v, want %q", c.rel, c.followLast, got, err, c.want)
		}
	}
	if _, err := resolveInRoot(root, "loop/a/x", false); err == nil || !strings.Contains(err.Error(), "too many levels") {
		t.Errorf("loop: %v", err)
	}
	if _, err := resolveInRoot(root, "missing/dir/file", false); err == nil {
		t.Error("missing parent accepted")
	}
}

func TestArchiveHandlersWithoutSupervisor(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "v1", "c", "d", t.TempDir(), "", "", "host", "auto")
	for _, h := range []func(w *httptest.ResponseRecorder){
		func(w *httptest.ResponseRecorder) {
			s.handleArchiveGet(w, httptest.NewRequest("GET", "/v1/containers/x/archive?path=/etc", nil).WithContext(context.Background()))
		},
		func(w *httptest.ResponseRecorder) {
			s.handleArchivePut(w, httptest.NewRequest("PUT", "/v1/containers/x/archive?path=/etc", strings.NewReader("")))
		},
	} {
		rec := httptest.NewRecorder()
		h(rec)
		if rec.Code != 503 {
			t.Errorf("no supervisor: %d %s", rec.Code, rec.Body)
		}
	}
}
