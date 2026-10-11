package health

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeCrun answers "exec" with the exit code stored in file code.
func fakeCrun(t *testing.T) (bin, codeFile string) {
	t.Helper()
	dir := t.TempDir()
	codeFile = filepath.Join(dir, "code")
	_ = os.WriteFile(codeFile, []byte("0"), 0o600)
	bin = filepath.Join(dir, "crun")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\nexit $(cat "+codeFile+")\n"), 0o755)
	return bin, codeFile
}

func TestProbes(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	bin, code := fakeCrun(t)
	for _, c := range []struct {
		cfg Config
		ok  bool
	}{
		{Config{}, true},
		{Config{Type: TypeNone}, true},
		{Config{Type: "smoke"}, false},
		{Config{Type: TypeExec}, false},
		{Config{Type: TypeExec, Command: []string{"true"}}, true},
		{Config{Type: TypeHTTP, URL: srv.URL + "/ok"}, true},
		{Config{Type: TypeHTTP, URL: srv.URL + "/bad"}, false},
		{Config{Type: TypeHTTP, URL: "http://127.0.0.1:1/"}, false},
		{Config{Type: TypeHTTP, URL: "::bad"}, false},
		{Config{Type: TypeTCP, Address: ln.Addr().String()}, true},
		{Config{Type: TypeTCP, Address: "127.0.0.1:1"}, false},
	} {
		if err := Probe(ctx, c.cfg, "c", bin, "/r"); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.cfg, err)
		}
	}
	_ = os.WriteFile(code, []byte("1"), 0o600)
	if err := Probe(ctx, Config{Type: TypeExec, Command: []string{"x"}}, "c", bin, "/r"); err == nil {
		t.Error("failing exec probe")
	}
}

func TestCheckerLifecycle(t *testing.T) {
	bin, code := fakeCrun(t)
	var unhealthy atomic.Int32
	cfg := Normalize(Config{Type: TypeExec, Command: []string{"x"}, Interval: 20 * time.Millisecond, Retries: 2, StartPeriod: 10 * time.Millisecond})
	c := New("c", cfg, bin, "/r", quiet, func(string) { unhealthy.Add(1) })
	c.Start(context.Background())
	wait := func(want Status) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for c.Status() != want {
			if time.Now().After(deadline) {
				t.Fatalf("status %s, want %s", c.Status(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(StatusHealthy)
	_ = os.WriteFile(code, []byte("1"), 0o600)
	wait(StatusUnhealthy)
	_ = os.WriteFile(code, []byte("0"), 0o600)
	wait(StatusHealthy) // recovered
	c.Stop()
	if unhealthy.Load() != 1 {
		t.Errorf("onUnhealthy called %d times", unhealthy.Load())
	}

	// Stopped during the start period; a type-less checker never probes.
	c2 := New("c", Config{Type: TypeExec, Command: []string{"x"}, Interval: time.Second, StartPeriod: time.Hour}, bin, "/r", quiet, nil)
	c2.Start(context.Background())
	c2.Stop()
	c3 := New("c", Normalize(Config{Interval: 10 * time.Millisecond}), bin, "/r", quiet, nil)
	c3.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	c3.Stop()
	(&Checker{}).Stop()
}

func TestWaitReadyEdges(t *testing.T) {
	bin, code := fakeCrun(t)
	cfg := Config{Type: TypeExec, Command: []string{"x"}, Timeout: time.Second, StartPeriod: 10 * time.Millisecond}
	if err := WaitReady(context.Background(), quiet, cfg, "c", bin, "/r"); err != nil {
		t.Fatal(err)
	}
	if err := WaitReady(context.Background(), quiet, Config{}, "c", bin, "/r"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg.StartPeriod = time.Hour
	if err := WaitReady(ctx, quiet, cfg, "c", bin, "/r"); err == nil {
		t.Error("cancelled during start period")
	}
	_ = os.WriteFile(code, []byte("1"), 0o600)
	cfg.StartPeriod = 0
	ctx, cancel = context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if err := WaitReady(ctx, nil, cfg, "c", bin, "/r"); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Errorf("never ready: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if err := WaitReady(ctx2, quiet, cfg, "c", bin, "/r"); err == nil {
		t.Error("cancelled before first probe")
	}
}
