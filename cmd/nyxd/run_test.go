package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/daemonlock"
	"github.com/zrougamed/nyxd/internal/rootless"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// cfgFor is a rootless host-network configuration under a fresh base dir.
func cfgFor(t *testing.T) Config {
	t.Helper()
	base := t.TempDir()
	return Config{BaseDir: base, CrunBin: "/bin/true", NetDriver: "host", DNS: "auto", LogLevel: "debug",
		Socket: filepath.Join(base, "nyxd.sock"), Rootless: rootless.Info{Rootless: true}}
}

func TestRunStartsAndStops(t *testing.T) {
	cfg := cfgFor(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, quiet) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(cfg.Socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A second daemon on the same base dir is refused.
	if err := run(context.Background(), cfg, quiet); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second daemon: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// The control socket cannot be created: logged, the daemon still runs.
	cfg = cfgFor(t)
	cfg.Socket = filepath.Join(cfg.BaseDir, strings.Repeat("s", 120), "nyxd.sock")
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := run(ctx, cfg, quiet); err != nil {
		t.Errorf("run without a control socket: %v", err)
	}
}

func TestRunStartupFailures(t *testing.T) {
	block := func(cfg Config, name string) Config {
		_ = os.WriteFile(filepath.Join(cfg.BaseDir, name), nil, 0o600)
		return cfg
	}
	for want, mk := range map[string]func() Config{
		"image store":         func() Config { return block(cfgFor(t), "images") },
		"overlay":             func() Config { return block(cfgFor(t), "overlay") },
		"log collector":       func() Config { return block(cfgFor(t), "logs") },
		"builder":             func() Config { return block(cfgFor(t), "build") },
		"runtime":             func() Config { c := cfgFor(t); c.CrunBin = "/nonexistent/crun"; return c },
		"unknown -net-driver": func() Config { c := cfgFor(t); c.NetDriver = "bogus"; return c },
		"needs root":          func() Config { c := cfgFor(t); c.NetDriver = "native"; return c },
		"network setup": func() Config {
			c := cfgFor(t)
			c.NetDriver = "cni"
			c.CNIConfDir = filepath.Join(c.BaseDir, "file", "net.d")
			return block(c, "file")
		},
		"cni runtime": func() Config {
			c := cfgFor(t)
			c.NetDriver = "cni"
			c.CNIConfDir = t.TempDir()
			c.CrunBin = "/nonexistent/crun"
			return c
		},
	} {
		cfg := mk()
		err := run(context.Background(), cfg, quiet)
		w := want
		if want == "cni runtime" {
			w = "runtime"
		}
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("%s: %v", want, err)
		}
	}
	// The lock itself cannot be created.
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	cfg := cfgFor(t)
	cfg.BaseDir = filepath.Join(f, "base")
	if err := run(context.Background(), cfg, quiet); err == nil {
		t.Error("base dir under a file")
	}
	// Rootful on a host whose cgroup layout is not unified v2 fails early;
	// on a v2 host it gets past the check and fails later (native needs root).
	cfg = cfgFor(t)
	cfg.Rootless = rootless.Info{}
	cfg.NetDriver = "bogus"
	if err := run(context.Background(), cfg, quiet); err == nil {
		t.Error("rootful with a bogus driver")
	}
}

func TestFlagsHelpers(t *testing.T) {
	for in, ok := range map[string]bool{"default": true, "100000:65536": true, "x": false, "1:": false, "0:5": false, "5:0": false, "a:b": false} {
		if _, _, err := parseRemap(in); (err == nil) != ok {
			t.Errorf("parseRemap(%q): %v", in, err)
		}
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv(rootless.EnvHostUID, "4242")
	var cfg Config
	applyRootlessDefaults(&cfg)
	if !strings.HasSuffix(cfg.BaseDir, filepath.Join(".local", "share", "nyxd")) || !strings.Contains(cfg.Socket, "nyxd-4242") || cfg.NetDriver != "host" {
		t.Errorf("defaults: %+v", cfg)
	}
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1")
	t.Setenv(rootless.EnvHostUID, "")
	cfg = Config{}
	applyRootlessDefaults(&cfg)
	if cfg.BaseDir != "/data/nyxd" || cfg.Socket != "/run/user/1/nyxd/nyxd.sock" {
		t.Errorf("XDG: %+v", cfg)
	}
	if hostUID() != os.Getuid() {
		t.Error("hostUID without env")
	}
	for _, l := range []string{"debug", "warn", "error", "info", "x"} {
		if newLogger(l) == nil {
			t.Error("logger")
		}
	}
}

func TestStdioProxyAndStop(t *testing.T) {
	if stdioProxy([]string{"-bogus"}) != 2 || stopCmd([]string{"-bogus"}) != 2 {
		t.Error("bad flags")
	}
	if stdioProxy([]string{"-socket", filepath.Join(t.TempDir(), "none.sock")}) != 1 {
		t.Error("proxy to a missing socket")
	}
	if stopCmd([]string{"-base-dir", t.TempDir()}) != 0 {
		t.Error("stop with no daemon")
	}
	bad := t.TempDir()
	_ = os.MkdirAll(filepath.Join(bad, "run"), 0o700)
	_ = os.WriteFile(filepath.Join(bad, "run", "nyxd-daemon.lock"), nil, 0o000)
	if os.Getuid() != 0 && stopCmd([]string{"-base-dir", bad}) != 1 {
		t.Error("unreadable lock")
	}
	// A "daemon" (this test binary) that holds the lock and ignores SIGTERM.
	base := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$")
	cmd.Env = append(os.Environ(), "NYXD_TEST_HOLD_LOCK="+base)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill() //nolint:errcheck
	deadline := time.Now().Add(10 * time.Second)
	for {
		if pid, _ := daemonlock.Holder(base); pid == cmd.Process.Pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not take the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stopCmd([]string{"-base-dir", base, "-timeout", "500ms"}) != 1 {
		t.Error("stop must time out on a daemon that ignores SIGTERM")
	}
}

func TestHelperHoldLock(t *testing.T) {
	base := os.Getenv("NYXD_TEST_HOLD_LOCK")
	if base == "" {
		t.Skip("helper process only")
	}
	signal.Ignore(syscall.SIGTERM)
	l, err := daemonlock.Acquire(base)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	time.Sleep(time.Minute)
}
