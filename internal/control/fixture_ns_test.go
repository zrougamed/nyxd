//go:build linux

package control_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/control"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"github.com/zrougamed/nyxd/internal/testutil/registry"
	"github.com/zrougamed/nyxd/pkg/oci"
	goruntime "runtime"
)

// These tests drive the control API of a real in-process daemon (crun,
// overlay, image store) inside a private user namespace.

func start(t *testing.T) *daemon.Daemon {
	t.Helper()
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	return d
}

func run(t *testing.T, d *daemon.Daemon, body map[string]any) {
	t.Helper()
	if _, ok := body["image"]; !ok {
		body["image"] = "test/app:1"
	}
	if code, out := d.Do(t, "POST", "/v1/containers/run", body); code != 200 {
		t.Fatalf("run %v: %d %s", body, code, out)
	}
}

func logsOf(t *testing.T, d *daemon.Daemon, id, query string) string {
	t.Helper()
	_, out := d.Do(t, "GET", "/v1/containers/"+id+"/logs"+query, nil)
	return out
}

func waitLog(t *testing.T, d *daemon.Daemon, id, want string) {
	t.Helper()
	daemon.WaitFor(t, 15*time.Second, id+" log "+want, func() bool { return strings.Contains(logsOf(t, d, id, ""), want) })
}

// raw sends a request with a non-JSON body and content type.
func raw(t *testing.T, d *daemon.Daemon, method, path, ctype string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://unix"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := d.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestControlContainerLifecycle(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)

	code, out := d.Do(t, "GET", "/v1/ping", nil)
	if code != 200 || !strings.Contains(out, "true") {
		t.Fatalf("ping %d %s", code, out)
	}

	// Run requests that are rejected before anything starts.
	for want, body := range map[string]map[string]any{
		"missing image":    {"image": " "},
		"publish":          {"image": "test/app:1", "publish": []string{"nope"}},
		"invalid protocol": {"image": "test/app:1", "ports": []map[string]any{{"hostPort": 1, "containerPort": 2, "protocol": "sctp"}}},
		"invalid port":     {"image": "test/app:1", "ports": []map[string]any{{"hostPort": 0, "containerPort": 2}}},
		"healthcheck":      {"image": "test/app:1", "healthcheck": map[string]any{"type": "exec"}},
	} {
		if code, out := d.Do(t, "POST", "/v1/containers/run", body); code != 400 || !strings.Contains(out, want) {
			t.Errorf("%s: %d %s", want, code, out)
		}
	}
	if code, _ := raw(t, d, "POST", "/v1/containers/run", "", strings.NewReader("{")); code != 400 {
		t.Errorf("bad json: %d", code)
	}

	// Long-running containers, one with ports (ignored on the host network) and a healthcheck.
	run(t, d, map[string]any{"id": "web-1", "args": []string{"sleep", "300"}, "restart": "no",
		"publish": []string{"8080:80"}, "ports": []map[string]any{{"hostPort": 9090, "containerPort": 90}},
		"healthcheck": map[string]any{"type": "exec", "command": []string{"true"}, "interval": "100ms", "retries": 3}})
	run(t, d, map[string]any{"id": "web-2", "args": []string{"trapterm"}, "restart": "never", "env": []string{"A=1"}, "hostname": "box"})
	waitLog(t, d, "web-2", "ready")

	code, out = d.Do(t, "GET", "/v1/containers?detail=1", nil)
	if code != 200 || !strings.Contains(out, `"web-1"`) || !strings.Contains(out, `"items"`) {
		t.Fatalf("list detail: %d %s", code, out)
	}
	if _, out = d.Do(t, "GET", "/v1/containers", nil); strings.Contains(out, "items") {
		t.Errorf("list without detail: %s", out)
	}

	// ID resolution (through exec, which answers at once): prefix, short id, ambiguity, unknown.
	short := supervisor.DisplayID("web-1")
	ids := []string{"web-1", "web-2"}
	hexPrefix := func() string { // a short-id prefix that matches exactly one container
		a, b := supervisor.DisplayID(ids[0]), supervisor.DisplayID(ids[1])
		for n := 1; n <= 12; n++ {
			if a[:n] != b[:n] {
				return a[:n]
			}
		}
		return a
	}()
	for ref, wantCode := range map[string]int{
		"web": 400, "nosuch": 404, short: 200, strings.ToUpper(short): 200, hexPrefix: 200, "web-1": 200,
		"fffffffffff0": 404, "0": 0, // "0": unique, ambiguous or absent depending on the hashes
	} {
		code, out := d.Do(t, "POST", "/v1/containers/"+ref+"/exec", map[string]any{"argv": []string{"true"}})
		if wantCode != 0 && code != wantCode {
			t.Errorf("resolve %q: %d %s", ref, code, out)
		}
	}
	// Both short ids share their first hex digit with something only by chance;
	// an ambiguous hex prefix is checked with a common prefix when there is one.
	if a, b := supervisor.DisplayID(ids[0]), supervisor.DisplayID(ids[1]); a[0] == b[0] {
		if code, _ := d.Do(t, "POST", "/v1/containers/"+a[:1]+"/exec", map[string]any{"argv": []string{"true"}}); code != 400 {
			t.Errorf("ambiguous short prefix: %d", code)
		}
	}

	// A unique prefix, and logs of an ambiguous one.
	run(t, d, map[string]any{"id": "solo-x", "args": []string{"sleep", "300"}, "restart": "no"})
	if code, out := d.Do(t, "POST", "/v1/containers/solo/exec", map[string]any{"argv": []string{"true"}}); code != 200 {
		t.Errorf("unique prefix: %d %s", code, out)
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/web/logs", nil); code != 400 {
		t.Errorf("logs of an ambiguous prefix: %d", code)
	}

	// A signal the workload survives: delivered, container keeps running.
	if code, out := d.Do(t, "POST", "/v1/containers/web-1/kill", map[string]any{"signal": "SIGCONT"}); code != 200 {
		t.Errorf("kill CONT: %d %s", code, out)
	}

	// exec: JSON body, streamed stdin, failures.
	if code, out := d.Do(t, "POST", "/v1/containers/web-1/exec", map[string]any{"argv": []string{"echo", "exec-ok"}}); code != 200 || !strings.Contains(out, "exec-ok") {
		t.Errorf("exec: %d %s", code, out)
	}
	hdr := `{"argv":["cat"]}` + "\nfrom stdin\n"
	if code, out := raw(t, d, "POST", "/v1/containers/web-1/exec", "application/x-nyxd-exec+v1", strings.NewReader(hdr)); code != 200 || !strings.Contains(out, "from stdin") {
		t.Errorf("exec stream: %d %s", code, out)
	}
	// nyx exec -i with a one-shot command: the client keeps its stdin open,
	// the response still ends when the command does.
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() { _, _ = pw.Write([]byte(`{"argv":["echo","stdin-open"]}` + "\n")) }()
	finished := make(chan string, 1)
	go func() {
		_, out := raw(t, d, "POST", "/v1/containers/web-1/exec", "application/x-nyxd-exec+v1", pr)
		finished <- out
	}()
	select {
	case out := <-finished:
		if !strings.Contains(out, "stdin-open") {
			t.Errorf("exec with open stdin: %q", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("exec did not return while the client kept stdin open")
	}
	if code, out := d.Do(t, "POST", "/v1/containers/web-1/exec", map[string]any{"argv": []string{"/nope"}}); code != 200 || !strings.Contains(out, "nyxd: exec:") {
		t.Errorf("exec failure: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/web-1/exec", map[string]any{"argv": []string{"echo", "tty"}, "tty": true, "rows": 30, "cols": 90}); code != 200 {
		t.Errorf("exec tty: %d %s", code, out)
	}
	for name, c := range map[string]struct{ ctype, body string }{
		"empty argv":   {"", `{"argv":[]}`},
		"bad json":     {"", `{`},
		"empty header": {"application/x-nyxd-exec+v1", "\n"},
		"bad header":   {"application/x-nyxd-exec+v1", "{\n"},
	} {
		if code, _ := raw(t, d, "POST", "/v1/containers/web-1/exec", c.ctype, strings.NewReader(c.body)); code != 400 {
			t.Errorf("exec %s: %d", name, code)
		}
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/nosuch/exec", map[string]any{"argv": []string{"x"}}); code != 404 {
		t.Errorf("exec unknown: %d", code)
	}

	// Logs: tail, raw JSON, follow until the container exits.
	run(t, d, map[string]any{"id": "chat", "args": []string{"chatty", "6"}, "restart": "no"})
	waitLog(t, d, "chat", "line 6")
	// stdout and stderr lines may interleave in any order: count them.
	if out := logsOf(t, d, "chat", "?tail=2"); strings.Count(out, "line ") != 2 {
		t.Errorf("tail=2: %q", out)
	}
	if out := logsOf(t, d, "chat", "?plain=0&tail=1"); !strings.HasPrefix(out, "{") {
		t.Errorf("plain=0: %q", out)
	}
	if out := logsOf(t, d, "chat", "?follow=1"); !strings.Contains(out, "line 1") {
		t.Errorf("follow finished container: %q", out)
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/a.b/logs", nil); code != 404 {
		t.Errorf("unsafe log id: %d", code)
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/never-ran/logs", nil); code != 404 {
		t.Errorf("no log: %d", code)
	}

	// Follow a running container until it stops.
	done := make(chan string)
	go func() {
		_, out := d.Do(t, "GET", "/v1/containers/web-2/logs?follow=true", nil)
		done <- out
	}()
	time.Sleep(300 * time.Millisecond)
	if code, out := d.Do(t, "POST", "/v1/containers/web-2/stop", nil); code != 200 {
		t.Fatalf("stop: %d %s", code, out)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "got TERM") {
			t.Errorf("follow output: %q", out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("follow did not end after stop")
	}

	// Kill, remove.
	if code, out := d.Do(t, "POST", "/v1/containers/web-1/kill", map[string]any{}); code != 200 || !strings.Contains(out, "KILL") {
		t.Errorf("kill: %d %s", code, out)
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/nosuch/remove", nil); code != 404 {
		t.Errorf("remove unknown: %d", code)
	}
	for _, id := range []string{"web-1", "web-2"} {
		daemon.WaitFor(t, 15*time.Second, id+" removable", func() bool {
			code, _ := d.Do(t, "POST", "/v1/containers/"+id+"/remove", nil)
			return code == 200 || code == 404
		})
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/web-1/kill", nil); code != 404 {
		t.Errorf("kill removed: %d", code)
	}

	// Streamed run of a local image; generated id.
	code, out = d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "test/app:1", "stream": true, "restart": "no"})
	if code != 200 || !strings.Contains(out, `"phase":"run"`) {
		t.Errorf("streamed run: %d %s", code, out)
	}
}

func TestControlRunPullFailures(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	// No network in the namespace: pulls fail fast.
	if code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "registry.invalid/x:1", "registry_username": "u"}); code != http.StatusBadGateway {
		t.Errorf("run with failing pull: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "registry.invalid/x:1", "stream": true}); code != 200 || !strings.Contains(out, `"phase":"error"`) {
		t.Errorf("streamed run with failing pull: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": "registry.invalid/x:1", "username": "u"}); code != http.StatusBadGateway {
		t.Errorf("pull: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": "registry.invalid/x:1", "stream": true}); code != 200 || !strings.Contains(out, `"phase":"error"`) {
		t.Errorf("streamed pull: %d %s", code, out)
	}
	if code, _ := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": " "}); code != 400 {
		t.Errorf("pull without ref: %d", code)
	}
	if code, _ := raw(t, d, "POST", "/v1/images/pull", "", strings.NewReader("{")); code != 400 {
		t.Errorf("pull bad json: %d", code)
	}
	// A start failure: the entrypoint does not exist in the image.
	d.Image(t, "test/broken:1", nil)
	if code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "test/broken:1", "id": "dup", "args": []string{"sleep", "60"}}); code != 200 {
		t.Fatalf("first run: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "test/broken:1", "id": "dup", "args": []string{"sleep", "60"}}); code != 400 {
		t.Errorf("duplicate id: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"image": "test/broken:1", "id": "dup", "stream": true}); code != 200 || !strings.Contains(out, `"phase":"error"`) {
		t.Errorf("streamed duplicate id: %d %s", code, out)
	}
}

func TestControlImages(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	d.Image(t, "test/other:2", nil)

	if code, out := d.Do(t, "GET", "/v1/images", nil); code != 200 || !strings.Contains(out, `"test/other:2"`) {
		t.Fatalf("images: %d %s", code, out)
	}
	// Tag.
	if code, out := d.Do(t, "POST", "/v1/images/tag", map[string]any{"source": "test/app:1", "target": "test/app:latest"}); code != 200 {
		t.Errorf("tag: %d %s", code, out)
	}
	if code, _ := d.Do(t, "POST", "/v1/images/tag", map[string]any{"source": "test/app:1"}); code != 400 {
		t.Errorf("tag without target: %d", code)
	}
	if code, _ := d.Do(t, "POST", "/v1/images/tag", map[string]any{"source": "nope:1", "target": "x:1"}); code != 404 {
		t.Errorf("tag unknown: %d", code)
	}
	if code, _ := raw(t, d, "POST", "/v1/images/tag", "", strings.NewReader("{")); code != 400 {
		t.Errorf("tag bad json: %d", code)
	}

	// Save, then load into a fresh name.
	code, archive := d.Do(t, "GET", "/v1/images/save?ref=test/app:1&ref=test/other:2", nil)
	if code != 200 || len(archive) < 1000 {
		t.Fatalf("save: %d (%d bytes)", code, len(archive))
	}
	if code, _ := d.Do(t, "GET", "/v1/images/save", nil); code != 400 {
		t.Errorf("save without ref: %d", code)
	}
	if code, _ := d.Do(t, "GET", "/v1/images/save?ref=nope:1", nil); code != 404 {
		t.Errorf("save unknown: %d", code)
	}
	if code, out := d.Do(t, "POST", "/v1/images/remove", map[string]any{"ref": "test/other:2"}); code != 200 {
		t.Fatalf("remove: %d %s", code, out)
	}
	if code, out := raw(t, d, "POST", "/v1/images/load", "application/x-tar", strings.NewReader(archive)); code != 200 || !strings.Contains(out, "other") {
		t.Errorf("load: %d %s", code, out)
	}
	if code, _ := raw(t, d, "POST", "/v1/images/load", "", strings.NewReader("not a tar")); code != 400 {
		t.Errorf("load garbage: %d", code)
	}

	// Remove errors.
	for name, body := range map[string]string{"bad json": "{", "no ref": `{"ref":""}`, "unknown": `{"ref":"nope:9"}`} {
		if code, _ := raw(t, d, "POST", "/v1/images/remove", "", strings.NewReader(body)); code != 400 {
			t.Errorf("remove %s: %d", name, code)
		}
	}

	// Push: unknown image, then a registry that does not answer.
	if code, _ := d.Do(t, "POST", "/v1/images/push", map[string]any{"ref": "nope:1"}); code != 404 {
		t.Errorf("push unknown: %d", code)
	}
	if code, _ := d.Do(t, "POST", "/v1/images/push", map[string]any{"ref": ""}); code != 400 {
		t.Errorf("push without ref: %d", code)
	}
	if code, _ := raw(t, d, "POST", "/v1/images/push", "", strings.NewReader("{")); code != 400 {
		t.Errorf("push bad json: %d", code)
	}
	if code, out := d.Do(t, "POST", "/v1/images/tag", map[string]any{"source": "test/app:1", "target": "127.0.0.1:1/app:1"}); code != 200 {
		t.Fatalf("tag for push: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/images/push", map[string]any{"ref": "127.0.0.1:1/app:1"}); code != 200 || !strings.Contains(out, `"phase":"error"`) {
		t.Errorf("push to closed port: %d %s", code, out)
	}

	// Prune: an image used by a container is kept.
	run(t, d, map[string]any{"id": "keep", "args": []string{"sleep", "60"}, "restart": "no"})
	if code, out := d.Do(t, "POST", "/v1/images/prune", map[string]any{"dry_run": true}); code != 200 || !strings.Contains(out, `"dry_run":true`) {
		t.Errorf("prune dry run: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/images/prune", nil); code != 200 || !strings.Contains(out, "other") {
		t.Errorf("prune: %d %s", code, out)
	}
	if _, out := d.Do(t, "GET", "/v1/images", nil); !strings.Contains(out, `"test/app:1"`) {
		t.Errorf("in-use image pruned: %s", out)
	}
}

func contextTar(t *testing.T, files map[string]string) io.Reader {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(data))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &b
}

func TestControlBuild(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	df := "FROM test/app:1\nARG WHO\nRUN echo built-$WHO > /built\nCMD [\"cat\", \"/built\"]\n"
	code, out := raw(t, d, "POST", "/v1/build?t=test/built:1&buildarg=WHO=nyx&nocache=1", "application/x-tar",
		contextTar(t, map[string]string{"Dockerfile": df}))
	if code != 200 || !strings.Contains(out, `"done":true`) {
		t.Fatalf("build: %d %s", code, out)
	}
	run(t, d, map[string]any{"id": "built", "image": "test/built:1", "restart": "no"})
	waitLog(t, d, "built", "built-nyx")

	code, out = raw(t, d, "POST", "/v1/build", "", contextTar(t, map[string]string{"Dockerfile": "FROM test/app:1\nRUN false\n"}))
	if code != 200 || !strings.Contains(out, `"error"`) {
		t.Errorf("failing build: %d %s", code, out)
	}
	if code, _ := raw(t, d, "POST", "/v1/build?buildarg=novalue", "", contextTar(t, nil)); code != 400 {
		t.Errorf("bad buildarg: %d", code)
	}
	if code, _ := raw(t, d, "POST", "/v1/build", "", strings.NewReader("not a tar at all, definitely")); code != 400 {
		t.Errorf("bad context: %d", code)
	}
}

func TestControlCompose(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	yaml := `services:
  app:
    image: test/app:1
    command: ["sleep", "300"]
    volumes: ["data:/data"]
  side:
    image: test/app:1
    command: ["sleep", "300"]
    depends_on: [app]
volumes:
  data: {}
`
	file := filepath.Join(dir, "proj", "compose.yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := d.Do(t, "POST", "/v1/compose/up", map[string]any{"file": file})
	if code != 200 || !strings.Contains(out, "proj-app") {
		t.Fatalf("up: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/stop", map[string]any{"file": file}); code != 200 || !strings.Contains(out, "proj-side") {
		t.Errorf("stop: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/stop", map[string]any{"file": file}); code != 200 {
		t.Errorf("stop again: %d %s", code, out)
	}
	code, out = d.Do(t, "POST", "/v1/compose/down", map[string]any{"file": file, "remove_volumes": true})
	if code != 200 || !strings.Contains(out, "removed_volume_paths") {
		t.Errorf("down -v: %d %s", code, out)
	}

	// Inline YAML with a context dir and an explicit project.
	body := map[string]any{"yaml": yaml, "context_dir": dir, "project": "inline"}
	if code, out := d.Do(t, "POST", "/v1/compose/up", body); code != 200 || !strings.Contains(out, "inline-app") {
		t.Fatalf("inline up: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/up", body); code != 200 {
		t.Errorf("second up of a running stack: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": yaml, "context_dir": dir}); code != 200 {
		t.Errorf("down of another project: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/down", body); code != 200 {
		t.Errorf("inline down: %d %s", code, out)
	}

	// Rejected documents.
	for name, body := range map[string]map[string]any{
		"neither":         {},
		"both":            {"file": file, "yaml": yaml},
		"no context":      {"yaml": yaml},
		"missing file":    {"file": filepath.Join(dir, "nope.yaml")},
		"bad yaml":        {"yaml": "services: [", "context_dir": dir},
		"unknown image":   {"yaml": "services:\n  a:\n    image: registry.invalid/x:1\n", "context_dir": dir},
		"unknown service": {"yaml": "services:\n  a:\n    image: test/app:1\n    depends_on: [b]\n", "context_dir": dir},
	} {
		for _, op := range []string{"up", "stop", "down"} {
			code, out := d.Do(t, "POST", "/v1/compose/"+op, body)
			if code != 400 && !(name == "unknown image" && op != "up") {
				t.Errorf("%s %s: %d %s", op, name, code, out)
			}
		}
	}
	for _, op := range []string{"up", "stop", "down"} {
		if code, _ := raw(t, d, "POST", "/v1/compose/"+op, "", strings.NewReader("{")); code != 400 {
			t.Errorf("%s bad json: %d", op, code)
		}
	}
}

func TestControlVersion(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := daemon.Start(t)
	code, out := d.Do(t, "GET", "/v1/version", nil)
	var v map[string]string
	if code != 200 || json.Unmarshal([]byte(out), &v) != nil || v["commit"] != "abc1234" {
		t.Fatalf("version: %d %s", code, out)
	}
}

func TestControlPullFromRegistry(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	src, err := image.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"1", "2", "3"} {
		cfg := &oci.ImageConfig{OS: "linux", Architecture: goruntime.GOARCH, Config: oci.ContainerConfig{Cmd: []string{"echo", "pulled-" + tag}, Env: []string{"PATH=/bin"}}}
		if err := daemon.StoreImage(src, "lib/app:"+tag, cfg, nil); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(src)
	defer reg.Close()

	if code, out := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": reg.Host + "/lib/app:1"}); code != 200 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("pull: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": reg.Host + "/lib/app:1", "stream": true}); code != 200 || !strings.Contains(out, `"phase":"done"`) {
		t.Errorf("streamed pull: %d %s", code, out)
	}
	// run pulls a missing image: streamed and plain.
	code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"id": "p2", "image": reg.Host + "/lib/app:2", "stream": true, "restart": "no"})
	if code != 200 || !strings.Contains(out, `"phase":"done"`) || !strings.Contains(out, `"phase":"run"`) {
		t.Errorf("run with streamed pull: %d %s", code, out)
	}
	waitLog(t, d, "p2", "pulled-2")
	run(t, d, map[string]any{"id": "p3", "image": reg.Host + "/lib/app:3", "restart": "no"})
	waitLog(t, d, "p3", "pulled-3")
	if code, _ := d.Do(t, "POST", "/v1/images/pull", map[string]any{"ref": reg.Host + "/lib/nope:1"}); code != http.StatusBadGateway {
		t.Errorf("pull unknown: %d", code)
	}
}

func TestControlLogFollowEdges(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "tail-me", "args": []string{"sh", "-c", "chatty 3; sleep 300"}, "restart": "no"})
	waitLog(t, d, "tail-me", "line 3")
	logPath := filepath.Join(d.Base, "logs", "tail-me.log")

	// The client goes away.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://unix/v1/containers/tail-me/logs?follow=1", nil)
	if resp, err := d.Client().Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	cancel()

	// The log file is truncated, then removed, then the container stops.
	done := make(chan string)
	go func() {
		_, out := d.Do(t, "GET", "/v1/containers/tail-me/logs?follow=1", nil)
		done <- out
	}()
	time.Sleep(300 * time.Millisecond)
	if err := os.Truncate(logPath, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if code, out := d.Do(t, "POST", "/v1/containers/tail-me/kill", nil); code != 200 {
		t.Fatalf("kill: %d %s", code, out)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("follow did not end")
	}
}

func TestControlComposeEdges(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	// A service that cannot start: unknown user.
	bad := map[string]any{"yaml": "services:\n  a:\n    image: test/app:1\n    user: nosuchuser\n", "context_dir": dir, "project": "bad"}
	if code, out := d.Do(t, "POST", "/v1/compose/up", bad); code != 400 {
		t.Errorf("unstartable service: %d %s", code, out)
	}

	// A named volume still bind-mounted by another project survives down -v.
	a := map[string]any{"yaml": "services:\n  s:\n    image: test/app:1\n    command: [sleep, '300']\n    volumes: [data:/data]\nvolumes:\n  data: {}\n", "context_dir": dir, "project": "vola"}
	if code, out := d.Do(t, "POST", "/v1/compose/up", a); code != 200 {
		t.Fatalf("up a: %d %s", code, out)
	}
	volPath := filepath.Join(d.Base, "volumes", "vola", "data")
	b := map[string]any{"yaml": "services:\n  s:\n    image: test/app:1\n    command: [sleep, '300']\n    volumes: ['" + volPath + ":/shared']\n", "context_dir": dir, "project": "volb"}
	if code, out := d.Do(t, "POST", "/v1/compose/up", b); code != 200 {
		t.Fatalf("up b: %d %s", code, out)
	}
	a["remove_volumes"] = true
	if code, out := d.Do(t, "POST", "/v1/compose/down", a); code != 200 || strings.Contains(out, "removed_volume_paths") {
		t.Errorf("down -v with the volume in use: %d %s", code, out)
	}
	if _, err := os.Stat(volPath); err != nil {
		t.Errorf("volume in use was deleted: %v", err)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/down", b); code != 200 {
		t.Errorf("down b: %d %s", code, out)
	}
}

func TestControlSocketGroupAsRoot(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	g, err := user.LookupGroupId("0")
	if err != nil {
		t.Skip(err)
	}
	sock := filepath.Join(t.TempDir(), "g.sock")
	s := control.New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "", "", "", "", sock, g.Name, "", "")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
