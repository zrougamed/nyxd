package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

func contextNames(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var names []string
	tr := tar.NewReader(buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestWriteContextEdges(t *testing.T) {
	ctx := t.TempDir()
	os.MkdirAll(filepath.Join(ctx, "sub"), 0o755)                                        //nolint:errcheck
	os.WriteFile(filepath.Join(ctx, "sub", "Dockerfile"), []byte("FROM scratch"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(ctx, "sub", "skip.txt"), []byte("x"), 0o644)              //nolint:errcheck
	os.WriteFile(filepath.Join(ctx, ".dockerignore"), []byte("sub\n"), 0o644)            //nolint:errcheck
	syscall.Mkfifo(filepath.Join(ctx, "fifo"), 0o644)                                    //nolint:errcheck

	// An ignored directory still sends the Dockerfile inside it; a fifo is
	// not part of the context.
	var buf bytes.Buffer
	if err := writeContext(&buf, ctx, filepath.Join(ctx, "sub", "Dockerfile"), "sub/Dockerfile"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(contextNames(t, &buf), ",")
	if got != ".dockerignore,sub/Dockerfile" {
		t.Errorf("context = %s", got)
	}

	if err := writeContext(&buf, ctx, filepath.Join(t.TempDir(), "Dockerfile"), injectedDockerfile); err == nil {
		t.Error("missing Dockerfile outside the context")
	}
	if err := writeContext(brokenWriter{}, ctx, filepath.Join(ctx, "sub", "Dockerfile"), "sub/Dockerfile"); err == nil {
		t.Error("write to a closed pipe")
	}
	if err := addToTar(tar.NewWriter(io.Discard), filepath.Join(ctx, "none"), "none"); err == nil {
		t.Error("addToTar of a missing file")
	}

	if os.Getuid() != 0 {
		locked := filepath.Join(ctx, "locked")
		os.Mkdir(locked, 0)                           //nolint:errcheck
		t.Cleanup(func() { os.Chmod(locked, 0o755) }) //nolint:errcheck
		if err := writeContext(io.Discard, ctx, filepath.Join(ctx, "sub", "Dockerfile"), "Dockerfile"); err == nil {
			t.Error("unreadable directory in the context")
		}
		os.Remove(locked)
		secret := filepath.Join(ctx, "secret")
		os.WriteFile(secret, []byte("s"), 0) //nolint:errcheck
		if err := writeContext(io.Discard, ctx, filepath.Join(ctx, "sub", "Dockerfile"), "Dockerfile"); err == nil {
			t.Error("unreadable file in the context")
		}
	}
}

func TestDoBuildArgs(t *testing.T) {
	sock := stub(t, reply(200, buildDone))
	ctx := t.TempDir()
	os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte("FROM scratch\n"), 0o644) //nolint:errcheck
	file := filepath.Join(ctx, "Dockerfile")
	if _, errOut, err := nyx(t, "-socket", sock, "build", "-f", "Dockerfile", ctx); err == nil {
		t.Errorf("relative -f outside the working directory accepted: %s", errOut)
	}
	if out, errOut, err := nyx(t, "-socket", sock, "build", "-f", file, "-t", "a:1", ctx); err != nil || !strings.Contains(out, "Successfully tagged t") {
		t.Errorf("build -f: %v %s %s", err, out, errOut)
	}
	if _, _, err := nyx(t, "-socket", sock, "build", file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file as context: %v", err)
	}
}
