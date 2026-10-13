package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
)

// stub serves h on a Unix socket, standing in for nyxd with scripted (often
// broken) answers.
func stub(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "stub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// reply answers every request with status and body (content type ndjson when
// the body has several lines).
func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Count(body, "\n") > 1 {
			w.Header().Set("Content-Type", "application/x-ndjson")
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}
}

// routes answers by path suffix; the first matching key wins.
func routes(m map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for suffix, h := range m {
			if strings.HasSuffix(r.URL.Path, suffix) {
				h(w, r)
				return
			}
		}
		http.NotFound(w, r)
	}
}

func TestClientAgainstBrokenDaemon(t *testing.T) {
	ctxDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	cfile := filepath.Join(t.TempDir(), "compose.yaml")
	_ = os.WriteFile(cfile, []byte("services:\n  s:\n    image: x\n"), 0o644)
	archive := filepath.Join(t.TempDir(), "a.tar")
	_ = os.WriteFile(archive, []byte("x"), 0o644)

	for _, c := range []struct {
		name  string
		h     http.HandlerFunc
		args  []string
		want  string // in error (or stderr); "" = must succeed
		inOut string // must appear in stdout+stderr
	}{
		{"ping 500", reply(500, "down"), []string{"ping"}, "ping: down", ""},
		{"version 500", reply(500, "down"), []string{"version"}, "version: down", ""},
		{"ps 500", reply(500, "store gone"), []string{"ps"}, "ps: store gone", ""},
		{"ps bad json", reply(200, "{"), []string{"ps"}, "decode", ""},
		{"ps formats", reply(200, `{"containers":["c"],"items":[{"id":"`+strings.Repeat("a", 40)+`","short_id":"","image":"`+strings.Repeat("i", 40)+`","status":"running"}]}`),
			[]string{"ps"}, "", "..."},
		{"ps -q ids", reply(200, `{"items":[{"id":"full-id","short_id":"","image":"x","status":"running"}]}`), []string{"ps", "-q"}, "", "full-id"},
		{"image ls 500", reply(500, "store gone"), []string{"image", "ls"}, "image ls: store gone", ""},
		{"image ls bad json", reply(200, "{"), []string{"image", "ls"}, "", ""},
		{"image prune 500", reply(500, "store gone"), []string{"image", "prune"}, "image prune: store gone", ""},
		{"image prune bad json", reply(200, "{"), []string{"image", "prune"}, "", ""},
		{"image prune nothing", reply(200, `{"ok":true,"removed":[]}`), []string{"image", "prune"}, "", "nothing to prune"},
		{"compose up bad json", reply(200, "{"), []string{"compose", "up", "-f", cfile}, "bad response", ""},
		{"compose up not ok", reply(200, `{"ok":false}`), []string{"compose", "up", "-f", cfile}, "bad response", ""},
		{"compose down bad", reply(200, "{"), []string{"compose", "down", "-f", cfile}, "bad response", ""},
		{"compose up 500", reply(500, "boom"), []string{"compose", "up", "-f", cfile}, "boom", ""},
		{"run 503", reply(503, ""), []string{"run", "-d", "x"}, "daemon unavailable", ""},
		{"run 418", reply(418, "tea"), []string{"run", "-d", "x"}, "unexpected HTTP 418", ""},
		{"run 400 empty", reply(400, ""), []string{"run", "-d", "x"}, "empty response body", ""},
		{"run bad json", reply(200, "{"), []string{"run", "-d", "x"}, "bad response", ""},
		{"run --json no newline", reply(200, `{"ok":true,"id":"c1","image":"x"}`), []string{"run", "-d", "--json", "x"}, "", "c1"},
		{"run stream decode", reply(200, "\n{bad\n\n"), []string{"run", "x"}, "decode", ""},
		{"run stream no id", reply(200, "{\"phase\":\"run\",\"ok\":true}\n\n"), []string{"run", "x"}, "missing container_id", ""},
		{"run stream error", reply(200, "{\"phase\":\"error\"}\n\n"), []string{"run", "x"}, "unknown error", ""},
		{"run stream closed", reply(200, "{\"phase\":\"layer\",\"digest\":\"sha256:0123456789abcdef\",\"cached\":true}\n{\"phase\":\"done\",\"ok\":true}\n"), []string{"run", "x"}, "closed before run phase", "already present"},
		{"run log stream fails", routes(map[string]http.HandlerFunc{
			"/run":  reply(200, "{\"phase\":\"run\",\"ok\":true,\"container_id\":\"c1\"}\n\n"),
			"/logs": reply(500, "no logs"),
		}), []string{"run", "x"}, "no logs", ""},
		{"pull --json 500", reply(500, "pull exploded"), []string{"pull", "--json", "x"}, "pull: pull exploded", ""},
		{"pull stream decode", reply(200, "\n{bad\n\n"), []string{"pull", "x"}, "decode", ""},
		{"pull stream error", reply(200, "{\"phase\":\"error\",\"message\":\"nope\"}\n\n"), []string{"pull", "x"}, "nope", ""},
		{"pull stream closed", reply(200, "{\"phase\":\"manifest\"}\n\n"), []string{"pull", "x"}, "closed before completion", ""},
		{"pull stream full", reply(200, strings.Join([]string{
			`{"phase":"resolve","ref":"docker.io/library/x:1"}`,
			`{"phase":"layer","index":0,"count":2,"digest":"sha256:aaaaaaaaaaaaaaaa","size":2000000000}`,
			`{"phase":"progress","digest":"sha256:aaaaaaaaaaaaaaaa","current":3000000000}`,
			`{"phase":"progress","digest":"sha256:aaaaaaaaaaaaaaaa","current":10}`,
			`{"phase":"layer_done","digest":"sha256:aaaaaaaaaaaaaaaa"}`,
			`{"phase":"layer","index":1,"count":2,"digest":"sha:short","cached":true}`,
			`{"phase":"done","ok":true,"config":{"entrypoint":["/e"],"cmd":["c"],"os":"linux","architecture":"amd64"}}`,
		}, "\n")+"\n"), []string{"pull", "x"}, "", "Entrypoint"},
		{"build stream", reply(200, "\n{bad}\n{\"stream\":\"step\\n\"}\n{\"error\":\"RUN failed\"}\n"), []string{"build", ctxDir}, "build failed: RUN failed", ""},
		{"build closed", reply(200, "{\"stream\":\"step\\n\"}\n\n"), []string{"build", ctxDir}, "connection closed", ""},
		{"build 500", reply(500, "no builder"), []string{"build", ctxDir}, "no builder", ""},
		{"logs 500", reply(500, "log store broken"), []string{"logs", "c"}, "logs: log store broken", ""},
		{"json error body", reply(500, `{"error":"daemon says no"}`), []string{"logs", "c"}, "logs: daemon says no", ""},
		{"empty error body", reply(503, ""), []string{"logs", "c"}, "service unavailable", ""},
		{"exec 500", reply(500, "no exec"), []string{"exec", "c", "true"}, "exec: no exec", ""},
		{"exec trailer", reply(200, "partial\nnyxd: exec: crun exec: exit status 3\n"), []string{"exec", "c", "x"}, "exit status 3", "partial"},
		{"exec bash missing", reply(200, "\nnyxd: exec: executable file `bash` not found in $PATH\n"), []string{"exec", "c", "bash"}, "", ""},
		{"save 500", reply(500, "x"), []string{"save", "x:1"}, "x", ""},
		{"load 500", reply(500, "bad archive"), []string{"load", "-i", archive}, "bad archive", ""},
		{"load bad json", reply(200, "{"), []string{"load", "-i", archive}, "", ""},
		{"tag 404", reply(404, "nope"), []string{"tag", "a", "b"}, "nope", ""},
		{"push stream error", reply(200, "{\"phase\":\"error\",\"message\":\"denied\"}\n\n"), []string{"push", "a:1"}, "denied", ""},
		{"push closed", reply(200, "{\"phase\":\"layer\"}\n\n"), []string{"push", "a:1"}, "", ""},
		{"stop 500", reply(500, "stop failed"), []string{"stop", "c"}, "stop failed", ""},
		{"rm 500", reply(500, "rm failed"), []string{"rm", "c"}, "rm failed", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			sock := stub(t, c.h)
			out, errOut, err := nyx(t, append([]string{"-socket", sock}, c.args...)...)
			all := out + errOut
			if c.want == "" && strings.Contains(c.name, "bad json") {
				// Either outcome is fine; the point is not to crash.
				return
			}
			if c.want == "" && c.name != "exec bash missing" && c.name != "push closed" && err != nil {
				t.Fatalf("err %v\n%s", err, all)
			}
			if c.want != "" && (err == nil || !strings.Contains(err.Error()+errOut, c.want)) {
				t.Fatalf("err %v, want %q\n%s", err, c.want, all)
			}
			if err != nil && (strings.Contains(err.Error(), "Internal Server Error") || strings.Contains(err.Error(), "Bad Request")) {
				t.Errorf("HTTP status shown to the user: %v", err)
			}
			if c.inOut != "" && !strings.Contains(all, c.inOut) {
				t.Fatalf("output lacks %q:\n%s", c.inOut, all)
			}
		})
	}
}

func TestExecWithTerminal(t *testing.T) {
	ptm, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptm.Close()
	defer tty.Close()
	_ = pty.Setsize(ptm, &pty.Winsize{Rows: 33, Cols: 99})
	var got string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		// Like nyxd's exec handler: stdin keeps streaming while we answer.
		_ = http.NewResponseController(w).EnableFullDuplex()
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		got = string(b[:n])
		_, _ = fmt.Fprint(w, "remote output")
	})
	stdioMu.Lock()
	old := os.Stdin
	os.Stdin = tty
	stdioMu.Unlock()
	defer func() { stdioMu.Lock(); os.Stdin = old; stdioMu.Unlock() }()
	go func() { time.Sleep(200 * time.Millisecond); _, _ = ptm.Write([]byte("x")) }()
	out, _, err := capture2(t, func() error { return run([]string{"-socket", sock, "exec", "-it", "c", "sh"}) })
	if err != nil || !strings.Contains(out, "remote output") || !strings.Contains(got, `"rows":33`) || !strings.Contains(got, `"cols":99`) {
		t.Fatalf("exec -it: %v %q header %q", err, out, got)
	}
}

// capture2 is capture without replacing os.Stdin.
func capture2(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	stdioMu.Lock()
	in := os.Stdin
	stdioMu.Unlock()
	return capture(t, "", func() error {
		os.Stdin = in
		return fn()
	})
}

func TestHelpers(t *testing.T) {
	var c pipeConn
	var a = c.LocalAddr()
	if a.Network() != "pipe" || a.String() != "wsl" || c.RemoteAddr() == nil ||
		c.SetDeadline(time.Time{}) != nil || c.SetReadDeadline(time.Time{}) != nil || c.SetWriteDeadline(time.Time{}) != nil {
		t.Error("pipeConn addr/deadlines")
	}
	hideWindow(nil)
	nudgeParentShellRedraw()
	if httpStatusNotFound(nil) || !httpStatusNotFound(errors.New("404 Not Found: x")) || httpStatusNotFound(errors.New("500")) {
		t.Error("httpStatusNotFound")
	}
	opts := execCLIOptions{WantTTY: true}
	if execErrIsBenignUserInterrupt(nil, opts) || !execErrIsBenignUserInterrupt(errors.New("exit status 130"), opts) ||
		execErrIsBenignUserInterrupt(errors.New("exit status 130"), execCLIOptions{}) {
		t.Error("execErrIsBenignUserInterrupt")
	}
	if execShellStdinHint(nil) || !execShellStdinHint([]string{"/bin/bash"}) || execShellStdinHint([]string{"ls"}) {
		t.Error("execShellStdinHint")
	}
	e127 := errors.New("exit status 127")
	if err := shellExit127Hint(e127, []string{"sh"}, []byte("x: not found"), true); err == nil || !strings.Contains(err.Error(), "exit 0") {
		t.Errorf("127 hint: %v", err)
	}
	for _, c := range []struct {
		err  error
		argv []string
		tail string
		tty  bool
	}{
		{errExecReported, []string{"sh"}, "not found", true},
		{nil, []string{"sh"}, "", true},
		{e127, []string{"sh"}, "", false},
		{errors.New("exit status 1"), []string{"sh"}, "not found", true},
		{e127, []string{"bash"}, "not found", true},
		{e127, []string{"sh"}, "fine", true},
	} {
		if got := shellExit127Hint(c.err, c.argv, []byte(c.tail), c.tty); got != c.err {
			t.Errorf("%+v: %v", c, got)
		}
	}

	t.Setenv("NYXD_SOCKET", "")
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	if os.Geteuid() != 0 {
		if defaultSocket() != systemSocket {
			t.Error("no rootless socket: system socket")
		}
		_ = os.MkdirAll(filepath.Join(rt, "nyxd"), 0o700)
		_ = os.WriteFile(filepath.Join(rt, "nyxd", "nyxd.sock"), nil, 0o600)
		if defaultSocket() != filepath.Join(rt, "nyxd", "nyxd.sock") {
			t.Error("rootless socket")
		}
	}
	t.Setenv("NYXD_SOCKET", "/x.sock")
	if defaultSocket() != "/x.sock" {
		t.Error("NYXD_SOCKET")
	}
	t.Setenv("NYX_WSL_EXE", "")
	if wslExe() != "wsl.exe" {
		t.Error("wslExe default")
	}
	if _, err := dialWSL(""); err == nil {
		t.Error("wsl endpoint without distro")
	}
	t.Setenv("NYX_WSL_EXE", "/nonexistent/wsl.exe")
	if _, err := dialWSL("nyx"); err == nil || !strings.Contains(err.Error(), "is WSL installed") {
		t.Errorf("missing wsl.exe: %v", err)
	}
	var sb strings.Builder
	lw := &limitedWriter{w: &sb, n: 3}
	if n, _ := lw.Write([]byte("hello")); n != 5 || sb.String() != "hel" {
		t.Error("limitedWriter truncates")
	}
	if n, _ := lw.Write([]byte("more")); n != 4 || sb.String() != "hel" {
		t.Error("limitedWriter after limit")
	}
	if humanSize(3<<30) == "" || shortDigest("short") != "short" {
		t.Error("humanSize/shortDigest")
	}
}

// Ctrl+C while the daemon is still starting the container: the first one
// kills the container once its id is known, a second one aborts at once.
func TestRunInterruptedBeforeStart(t *testing.T) {
	sigc := make(chan os.Signal, 1) // SIGINT must never take the default action here
	signal.Notify(sigc, os.Interrupt)
	defer signal.Stop(sigc)
	p, _ := os.FindProcess(os.Getpid())

	for _, abort := range []bool{false, true} {
		arrived, release := make(chan struct{}), make(chan struct{})
		var killed atomic.Bool
		sock := stub(t, routes(map[string]http.HandlerFunc{
			"/run": func(w http.ResponseWriter, r *http.Request) {
				close(arrived)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = fmt.Fprint(w, "{\"phase\":\"run\",\"container_id\":\"c1\"}\n")
			},
			"/kill": func(w http.ResponseWriter, r *http.Request) {
				killed.Store(true)
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			},
		}))
		done := make(chan error, 1)
		var errOut string
		go func() {
			var err error
			_, errOut, err = capture2(t, func() error { return doRun(sock, []string{"img"}) })
			done <- err
		}()
		<-arrived
		_ = p.Signal(os.Interrupt)
		time.Sleep(200 * time.Millisecond)
		if abort {
			_ = p.Signal(os.Interrupt)
		} else {
			close(release)
		}
		select {
		case err := <-done:
			if abort && (err == nil || killed.Load()) {
				t.Errorf("abort: err=%v killed=%v", err, killed.Load())
			}
			if !abort && (err != nil || !killed.Load() || !strings.Contains(errOut, "interrupted")) {
				t.Errorf("kill once started: err=%v killed=%v stderr=%q", err, killed.Load(), errOut)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("abort=%v: run did not end", abort)
		}
		if abort {
			close(release)
		}
	}
}
