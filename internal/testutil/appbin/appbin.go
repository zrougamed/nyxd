// Package appbin builds internal/testutil/testapp, a static multi-call binary
// standing in for sh, echo, cat, sleep, ... in test containers, and lays it
// out as an image layer, a root filesystem or a ready OCI bundle.
package appbin

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// Apps are the commands the test image provides in /bin (see testapp).
var Apps = []string{"sh", "echo", "cat", "sleep", "id", "true", "false", "env", "pwd", "touch", "mkdir",
	"hostname", "ignoreterm", "trapterm", "serve", "chatty", "lookup"}

// Layer returns the test image's root filesystem as an uncompressed tar.
func Layer(extra map[string]string) ([]byte, error) {
	app, err := TestApp()
	if err != nil {
		return nil, err
	}
	bin, err := os.ReadFile(app)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	mt := time.Unix(1700000000, 0)
	dir := func(name string, mode int64) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: mode, ModTime: mt})
	}
	file := func(name string, mode int64, data []byte) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(data)), ModTime: mt})
		_, _ = tw.Write(data)
	}
	for _, d := range []string{"bin", "etc", "root", "proc", "sys", "dev", "run", "home", "home/app"} {
		dir(d, 0o755)
	}
	dir("tmp", 0o1777)
	file("bin/testapp", 0o755, bin)
	for _, a := range Apps {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "bin/" + a, Linkname: "testapp", Mode: 0o777, ModTime: mt})
	}
	file("etc/passwd", 0o644, []byte("root:x:0:0:root:/root:/bin/sh\napp:x:1000:1000:app:/home/app:/bin/sh\n"))
	file("etc/group", 0o644, []byte("root:x:0:\napp:x:1000:\n"))
	names := make([]string, 0, len(extra))
	for n := range extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		file(strings.TrimPrefix(n, "/"), 0o644, []byte(extra[n]))
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	appOnce sync.Once
	appPath string
	appErr  error
)

// TestApp builds internal/testutil/testapp (static) once per process.
func TestApp() (string, error) {
	appOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nyxd-testapp-")
		if err != nil {
			appErr = err
			return
		}
		appPath = filepath.Join(dir, "testapp")
		cmd := exec.Command("go", "build", "-o", appPath, "github.com/zrougamed/nyxd/internal/testutil/testapp")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			appErr = &buildError{out: string(out), err: err}
		}
	})
	return appPath, appErr
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return "build testapp: " + e.err.Error() + ": " + e.out }

// Rootfs writes the test image's root filesystem into dir.
func Rootfs(dir string) error {
	layer, err := Layer(nil)
	if err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(layer))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dir, h.Name)
		switch h.Typeflag {
		case tar.TypeDir:
			mode := os.FileMode(h.Mode & 0o777)
			if h.Mode&0o1000 != 0 {
				mode |= os.ModeSticky
			}
			if err = os.MkdirAll(p, 0o755); err == nil {
				err = os.Chmod(p, mode)
			}
		case tar.TypeSymlink:
			err = os.Symlink(h.Linkname, p)
		default:
			var f *os.File
			if f, err = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)); err == nil {
				_, err = io.Copy(f, tr)
				f.Close()
			}
		}
		if err != nil {
			return err
		}
	}
}

// Bundle writes an OCI bundle under dir/<id> running args in the test
// rootfs (host network, rootless), for driving crun directly.
func Bundle(t *testing.T, dir, id string, args ...string) string {
	t.Helper()
	b := filepath.Join(dir, id)
	rootfs := filepath.Join(b, "rootfs")
	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Rootfs(rootfs); err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{Config: oci.ContainerConfig{Env: []string{"PATH=/bin"}}}
	if _, err := bundle.Generate(b, bundle.Options{ContainerID: id, RootFS: rootfs, ImageConfig: cfg, Args: args,
		HostNetwork: true, Rootless: true}); err != nil {
		t.Fatal(err)
	}
	return b
}
