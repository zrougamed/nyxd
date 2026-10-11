package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

func TestParseRestartPolicy(t *testing.T) {
	for in, want := range map[string]supervisor.RestartPolicy{
		"": supervisor.RestartUnlessStopped, "unless_stopped": supervisor.RestartUnlessStopped,
		"Always": supervisor.RestartAlways, "on-failure": supervisor.RestartOnFailure, "on_failure": supervisor.RestartOnFailure,
		"no": supervisor.RestartNever, "never": supervisor.RestartNever, "bogus": supervisor.RestartNever,
	} {
		if got := parseRestartPolicy(in); got != want {
			t.Errorf("%q = %s, want %s", in, got, want)
		}
	}
}

func TestParseRunHealthcheck(t *testing.T) {
	if c, err := parseRunHealthcheck(nil); c != nil || err != nil {
		t.Fatal("nil healthcheck")
	}
	ok := []runHealthJSON{
		{Type: "exec", Command: []string{"true"}, Interval: "1s", Timeout: "2s", StartPeriod: "3s", Retries: 2},
		{Type: "HTTP", URL: " http://x/ "},
		{Type: "tcp", Address: "127.0.0.1:1"},
		{Type: "none"},
	}
	for _, j := range ok {
		c, err := parseRunHealthcheck(&j)
		if err != nil || c == nil {
			t.Errorf("%+v: %v", j, err)
		}
	}
	c, _ := parseRunHealthcheck(&ok[0])
	if c.Interval != time.Second || c.Timeout != 2*time.Second || c.StartPeriod != 3*time.Second || c.Type != health.TypeExec {
		t.Errorf("parsed %+v", c)
	}
	for want, j := range map[string]runHealthJSON{
		"missing type":     {},
		"interval":         {Type: "exec", Command: []string{"x"}, Interval: "x"},
		"timeout":          {Type: "exec", Command: []string{"x"}, Timeout: "x"},
		"start_period":     {Type: "exec", Command: []string{"x"}, StartPeriod: "x"},
		"requires command": {Type: "exec"},
		"requires url":     {Type: "http"},
		"requires address": {Type: "tcp"},
		"unknown type":     {Type: "smoke"},
	} {
		if _, err := parseRunHealthcheck(&j); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestPullAuth(t *testing.T) {
	if pullAuthFromPullBody(&pullRequest{}) != nil || pullAuthFromRunBody(&runRequest{}) != nil {
		t.Error("auth without username")
	}
	if a := pullAuthFromPullBody(&pullRequest{Username: " u ", Password: "p"}); a.Username != "u" || a.Password != "p" {
		t.Error("pull auth")
	}
	if a := pullAuthFromRunBody(&runRequest{RegistryUsername: "u", RegistryPassword: "p"}); a.Username != "u" {
		t.Error("run auth")
	}
}

func TestGenerateContainerID(t *testing.T) {
	for in, prefix := range map[string]string{
		"docker.io/library/alpine:3.20": "docker-io-library-alpine-3-20-",
		"a b@sha256":                    "a-b-sha256-",
		"///":                           "c-",
		strings.Repeat("x", 60):         strings.Repeat("x", 48) + "-",
	} {
		if got := generateContainerID(in); !strings.HasPrefix(got, prefix) {
			t.Errorf("%q -> %q, want prefix %q", in, got, prefix)
		}
	}
}

func TestLogHelpers(t *testing.T) {
	if tailLines("", 5) != nil {
		t.Error("empty")
	}
	if got := tailLines("a\nb\nc\n", 2); strings.Join(got, ",") != "b,c" {
		t.Errorf("tail %v", got)
	}
	if got := tailLines("a\nb", 0); len(got) != 2 {
		t.Errorf("tail all %v", got)
	}
	var b bytes.Buffer
	writeLogLine(&b, []byte(`{"log":"hi"}`), true)
	writeLogLine(&b, []byte(`{"log":"nl\n"}`), true)
	writeLogLine(&b, []byte(`not json`), true)
	writeLogLine(&b, []byte(`{"log":"raw"}`), false)
	if b.String() != "hi\nnl\nnot json\n{\"log\":\"raw\"}\n" {
		t.Errorf("log lines %q", b.String())
	}
}

func TestReadExecJSONLine(t *testing.T) {
	rd := func(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
	if b, err := readExecJSONLine(rd(`{"argv":["x"]}`+"\nstdin"), 100); err != nil || string(b) != `{"argv":["x"]}` {
		t.Errorf("%s %v", b, err)
	}
	if b, err := readExecJSONLine(rd(`{"a":1}`), 100); err != nil || string(b) != `{"a":1}` {
		t.Errorf("eof line: %s %v", b, err)
	}
	if _, err := readExecJSONLine(rd(strings.Repeat("x", 20)+"\n"), 10); err == nil {
		t.Error("long line")
	}
	if _, err := readExecJSONLine(rd("\n"), 10); err == nil {
		t.Error("empty line")
	}
	if _, err := readExecJSONLine(bufio.NewReader(errReader{}), 10); err == nil {
		t.Error("read error")
	}
	if _, err := readExecJSONLine(bufio.NewReader(io.MultiReader(strings.NewReader("{}"), errReader{})), 10); err == nil {
		t.Error("read error after data")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestExecTTYWinSizeDefaults(t *testing.T) {
	if execTTYWinSize(execRequest{}) != nil || execTTYWinSize(execRequest{TTY: true}) != nil {
		t.Error("no size")
	}
	if w := execTTYWinSize(execRequest{TTY: true, Rows: 40}); w.Rows != 40 || w.Cols != 80 {
		t.Errorf("%+v", w)
	}
	if w := execTTYWinSize(execRequest{TTY: true, Cols: 100}); w.Rows != 24 || w.Cols != 100 {
		t.Errorf("%+v", w)
	}
}

func TestResolveHelpers(t *testing.T) {
	for s, want := range map[string]bool{"": false, "abcDEF09": true, "xyz": false} {
		if isAllHexDigits(s) != want {
			t.Errorf("hex %q", s)
		}
	}
	for s, want := range map[string]bool{"ok-id_1": true, "": false, "a.b": false, "a/b": false, "a b": false, "a$": false, strings.Repeat("a", 300): false} {
		if safeLogContainerID(s) != want {
			t.Errorf("safe %q", s)
		}
	}
	for err, code := range map[error]int{errNoSuchContainerID: 404, errAmbiguousContainerID: 400, errors.New("x"): 500} {
		rec := httptest.NewRecorder()
		writeResolveError(rec, err)
		if rec.Code != code {
			t.Errorf("%v -> %d", err, rec.Code)
		}
	}
	s := &Server{}
	if id, err := s.resolveContainerID(" raw "); err != nil || id != "raw" {
		t.Error("no supervisor: raw id")
	}
	if _, err := s.resolveContainerID(" "); err == nil {
		t.Error("empty id")
	}
	if s.containerInSupervisor("x") {
		t.Error("no supervisor")
	}
}

// A server without supervisor, store, runtime or builder answers 503 (or a
// plain error) instead of crashing.
func TestServerWithoutBackends(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s", "nyxd.sock")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(log, nil, nil, nil, nil, nil, "v1", "c", "d", dir, sock, "", "host", "auto")
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal("shutdown before start")
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, "http://unix"+path, strings.NewReader(body))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, r := range []struct{ method, path string }{
		{"POST", "/v1/containers/run"}, {"POST", "/v1/compose/up"}, {"POST", "/v1/compose/stop"},
		{"POST", "/v1/compose/down"}, {"POST", "/v1/containers/x/stop"}, {"POST", "/v1/containers/x/remove"},
		{"POST", "/v1/containers/x/kill"}, {"GET", "/v1/images"}, {"POST", "/v1/images/remove"},
		{"POST", "/v1/images/prune"}, {"POST", "/v1/images/pull"}, {"POST", "/v1/images/tag"},
		{"POST", "/v1/images/push"}, {"GET", "/v1/images/save?ref=x"}, {"POST", "/v1/images/load"},
		{"POST", "/v1/build"},
	} {
		if code, body := do(r.method, r.path, "{}"); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: %d %s", r.method, r.path, code, body)
		}
	}
	if code, body := do("GET", "/v1/containers?detail=1", ""); code != 200 || !strings.Contains(body, `"containers":null`) {
		t.Errorf("containers: %d %s", code, body)
	}
	if code, body := do("GET", "/v1/version", ""); code != 200 || !strings.Contains(body, `"version":"v1"`) {
		t.Errorf("version: %d %s", code, body)
	}
	if code, _ := do("GET", "/v1/containers/x/logs", ""); code != 404 {
		t.Errorf("logs without file: %d", code)
	}
	// A log path that cannot be read as a file.
	if err := os.MkdirAll(filepath.Join(dir, "logs", "d.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _ := do("GET", "/v1/containers/d/logs", ""); code != 500 {
		t.Errorf("unreadable log: %d", code)
	}
	// With a store but no supervisor, prune still refuses.
	store, err := image.NewStore(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	s.store = store
	if code, _ := do("POST", "/v1/images/prune", "{}"); code != http.StatusServiceUnavailable {
		t.Errorf("prune without supervisor: %d", code)
	}
}

func TestServerStartErrors(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := New(log, nil, nil, nil, nil, nil, "", "", "", "", "", "", "", "").Start(); err != nil {
		t.Error("empty socket must disable the API")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(log, nil, nil, nil, nil, nil, "", "", "", "", filepath.Join(dir, "file", "x", "s.sock"), "", "", "").Start(); err == nil {
		t.Error("socket dir under a file")
	}
	if err := New(log, nil, nil, nil, nil, nil, "", "", "", "", filepath.Join(dir, strings.Repeat("l", 120), "s.sock"), "", "", "").Start(); err == nil {
		t.Error("socket path too long")
	}
	if err := New(log, nil, nil, nil, nil, nil, "", "", "", "", filepath.Join(dir, "g.sock"), "no-such-group-nyx", "", "").Start(); err == nil {
		t.Error("unknown socket group")
	}
	g, err := user.LookupGroupId(itoa(os.Getgid()))
	if err != nil {
		t.Skip(err)
	}
	s := New(log, nil, nil, nil, nil, nil, "", "", "", "", filepath.Join(dir, "g2.sock"), g.Name, "", "")
	err = s.Start()
	if os.Getuid() == 0 {
		if err != nil {
			t.Errorf("socket group as root: %v", err)
		}
		_ = s.Shutdown(context.Background())
	} else if err == nil || !strings.Contains(err.Error(), "chown") {
		t.Errorf("socket chown to root as a user: %v", err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
