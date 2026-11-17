//go:build linux

package control_test

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlCopyArchive(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "cp1", "args": []string{"sleep", "300"}, "restart": "no"})
	c := d.Client()
	archive := func(method, p string, extra string, body io.Reader) (int, []byte) {
		u := "http://unix/v1/containers/cp1/archive?path=" + url.QueryEscape(p) + extra
		req, _ := http.NewRequest(method, u, body)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	tarOf := func(entries ...[2]string) *bytes.Buffer {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, e := range entries {
			_ = tw.WriteHeader(&tar.Header{Name: e[0], Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e[1]))})
			_, _ = tw.Write([]byte(e[1]))
		}
		_ = tw.Close()
		return &buf
	}
	readOne := func(b []byte) (string, string) {
		tr := tar.NewReader(bytes.NewReader(b))
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("archive: %v (%q)", err, b)
		}
		body, _ := io.ReadAll(tr)
		return h.Name, string(body)
	}

	// Into an existing directory, then read back.
	if code, out := archive("PUT", "/tmp", "", tarOf([2]string{"hello.txt", "hi"})); code != 200 {
		t.Fatalf("put into dir: %d %s", code, out)
	}
	if code, b := archive("GET", "/tmp/hello.txt", "", nil); code != 200 {
		t.Fatalf("get: %d %s", code, b)
	} else if name, body := readOne(b); name != "hello.txt" || body != "hi" {
		t.Fatalf("got %s=%q", name, body)
	}
	// To a new name: the top-level entry is renamed.
	if code, out := archive("PUT", "/tmp/renamed.txt", "", tarOf([2]string{"whatever.txt", "x"})); code != 200 {
		t.Fatalf("put rename: %d %s", code, out)
	}
	if _, b := archive("GET", "/tmp/renamed.txt", "", nil); !strings.Contains(string(b), "renamed.txt") {
		t.Fatalf("renamed: %q", b)
	}
	// A raw stream needs an existing directory.
	if code, _ := archive("PUT", "/tmp/nodir", "&raw=1", tarOf([2]string{"a", "b"})); code != 400 {
		t.Errorf("raw into missing dir: %d", code)
	}
	// An archive entry with ".." is cleaned against the destination (as
	// Docker does): it lands in /tmp, never above it.
	if code, out := archive("PUT", "/tmp", "", tarOf([2]string{"../../../escape.txt", "x"})); code != 200 {
		t.Fatalf("dotdot: %d %s", code, out)
	}
	if code, _ := archive("GET", "/tmp/escape.txt", "", nil); code != 200 {
		t.Errorf("../ entry should land in the destination: %d", code)
	}
	for p, want := range map[string]int{"/nope": 404, "/proc/self": 400, "relative": 400} {
		if code, _ := archive("GET", p, "", nil); code != want {
			t.Errorf("GET %s: %d, want %d", p, code, want)
		}
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/nosuch/archive?path=/", nil); code != 404 {
		t.Errorf("unknown container: %d", code)
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/cp1/archive", nil); code != 400 {
		t.Errorf("no path: %d", code)
	}
	// A whole directory, and the container root under the container's name.
	if code, b := archive("GET", "/tmp", "", nil); code != 200 || !strings.Contains(string(b), "tmp/hello.txt") {
		t.Errorf("dir: %d", code)
	}
	if code, b := archive("GET", "/", "", nil); code != 200 || !strings.Contains(string(b), "cp1/tmp/hello.txt") {
		t.Errorf("root: %d", code)
	}
	if code, _ := archive("PUT", "/no/such/dir/file", "", tarOf([2]string{"a", "b"})); code != 404 {
		t.Errorf("missing parent: %d", code)
	}
	if code, _ := archive("PUT", "/tmp", "", strings.NewReader("this is not a tar archive at all......")); code != 400 {
		t.Errorf("garbage archive: %d", code)
	}
	d.Do(t, "POST", "/v1/containers/cp1/stop", nil)
	if code, out := archive("GET", "/tmp", "", nil); code != 409 || !strings.Contains(string(out), "not running") {
		t.Errorf("stopped: %d %s", code, out)
	}
}
