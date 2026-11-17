package main

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCpSpec(t *testing.T) {
	for arg, want := range map[string][3]string{
		"web:/etc/hosts": {"web", "/etc/hosts", "true"},
		"web:relative":   {"web", "relative", "true"},
		"./a:b":          {"", "./a:b", "false"},
		"/abs/x:y":       {"", "/abs/x:y", "false"},
		"-":              {"", "-", "false"},
		"plain":          {"", "plain", "false"},
		":nocontainer":   {"", ":nocontainer", "false"},
	} {
		c, p, remote := cpSpec(arg)
		if c != want[0] || p != want[1] || (remote && want[2] != "true") || (!remote && want[2] != "false") {
			t.Errorf("%q: %q %q %v", arg, c, p, remote)
		}
	}
}

func TestCpArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a"}, "usage"},
		{[]string{"a", "b"}, "one of SRC and DEST"},
		{[]string{"x:/a", "y:/b"}, "between two containers"},
		{[]string{"--bogus", "x:/a", "."}, "unknown flag"},
	} {
		if err := doCp("/nonexistent.sock", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if err := doCp("/nonexistent.sock", []string{"--help"}); err != nil {
		t.Error(err)
	}
	if !strings.Contains(archiveURL("my web", "/a b", nil), "/v1/containers/my%20web/archive?path=%2Fa+b") {
		t.Error(archiveURL("my web", "/a b", nil))
	}
}

func tarBytes(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		body := h.Linkname
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
			h.Linkname = ""
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(body))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

func TestCpFromContainer(t *testing.T) {
	// The stub serves a directory "conf" with a file; Linkname carries the body.
	archive := tarBytes(t,
		tar.Header{Name: "conf/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "conf/app.ini", Typeflag: tar.TypeReg, Mode: 0o644, Linkname: "k=v"},
	)
	var gotPath string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		if r.URL.Query().Get("path") == "/stopped" {
			http.Error(w, "container is not running: web", http.StatusConflict)
			return
		}
		_, _ = w.Write(archive)
	})
	dir := t.TempDir()

	// Into an existing directory.
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "cp", "web:/etc/conf", dir}) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "conf", "app.ini")); string(b) != "k=v" || gotPath != "/etc/conf" {
		t.Fatalf("into dir: %q (path %q)", b, gotPath)
	}
	// Under a new name.
	dst := filepath.Join(dir, "renamed")
	if _, stderr, err := capture(t, "", func() error { return run([]string{"-socket", sock, "cp", "web:/etc/conf", dst}) }); err != nil || !strings.Contains(stderr, "Copied") {
		t.Fatalf("rename: %v %q", err, stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "app.ini")); string(b) != "k=v" {
		t.Fatalf("renamed: %q", b)
	}
	// A tar stream on stdout, unchanged.
	out, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "cp", "web:/etc/conf", "-"}) })
	if err != nil || out != string(archive) {
		t.Fatalf("stdout stream: %v (%d bytes)", err, len(out))
	}
	if err := run([]string{"-socket", sock, "cp", "web:/stopped", dir}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("daemon error: %v", err)
	}
	if err := run([]string{"-socket", sock, "cp", "web:/etc/conf", filepath.Join(dir, "no", "such", "x")}); err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("missing parent: %v", err)
	}
}

func TestCpToContainer(t *testing.T) {
	var got []byte
	var query string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "want PUT", 400)
			return
		}
		query = r.URL.RawQuery
		got, _ = io.ReadAll(r.Body)
		if r.URL.Query().Get("path") == "/ro" {
			http.Error(w, "copy into /ro: read-only", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	src := filepath.Join(t.TempDir(), "fix.sh")
	if err := os.WriteFile(src, []byte("echo fixed"), 0o755); err != nil {
		t.Fatal(err)
	}
	headers := func() []*tar.Header {
		var hs []*tar.Header
		tr := tar.NewReader(bytes.NewReader(got))
		for {
			h, err := tr.Next()
			if err != nil {
				return hs
			}
			hs = append(hs, h)
		}
	}
	// Default: owned by root inside the container.
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "cp", src, "web:/usr/local/bin/"}) }); err != nil {
		t.Fatal(err)
	}
	if hs := headers(); len(hs) != 1 || hs[0].Name != "fix.sh" || hs[0].Uid != 0 || hs[0].Gid != 0 || !strings.Contains(query, "path=%2Fusr%2Flocal%2Fbin%2F") {
		t.Fatalf("upload: %+v %s", hs, query)
	}
	// -a keeps the file's ids.
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "cp", "-a", src, "web:/tmp/"}) }); err != nil {
		t.Fatal(err)
	}
	if hs := headers(); len(hs) != 1 || hs[0].Uid != os.Getuid() {
		t.Fatalf("-a: %+v", hs)
	}
	// A tar stream from stdin goes as is, with raw=1.
	stream := string(tarBytes(t, tar.Header{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644, Linkname: "y"}))
	if _, _, err := capture(t, stream, func() error { return run([]string{"-socket", sock, "cp", "-", "web:/tmp"}) }); err != nil {
		t.Fatal(err)
	}
	if string(got) != stream || !strings.Contains(query, "raw=1") {
		t.Fatalf("stdin stream: %q %s", got, query)
	}
	if err := run([]string{"-socket", sock, "cp", src, "web:/ro"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("daemon error: %v", err)
	}
	if err := run([]string{"-socket", sock, "cp", filepath.Join(t.TempDir(), "missing"), "web:/tmp"}); err == nil {
		t.Fatal("missing source accepted")
	}
}
