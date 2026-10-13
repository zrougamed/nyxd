//go:build linux

package supervisor_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"github.com/zrougamed/nyxd/pkg/oci"
	"golang.org/x/sys/unix"
)

// These tests run real containers (crun, overlay) in a private user
// namespace through an in-process daemon.

func setup(t *testing.T) *daemon.Daemon {
	t.Helper()
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	return d
}

// spec builds a ContainerSpec for the test image; state is bind-mounted at
// /state so a workload can keep files across restarts.
func spec(t *testing.T, d *daemon.Daemon, id string, policy supervisor.RestartPolicy, state string, args ...string) supervisor.ContainerSpec {
	t.Helper()
	m, cfg, paths, err := d.Store.ResolvePulledImage("test/app:1")
	if err != nil {
		t.Fatal(err)
	}
	sp := supervisor.ContainerSpec{
		ID: id, Image: "test/app:1", ImageConfig: cfg, ManifestLayers: m.Layers, BlobPaths: paths,
		Args: args, RestartPolicy: policy,
	}
	if state != "" {
		sp.ExtraMounts = []bundle.Mount{{Type: "bind", Source: state, Destination: "/state", Options: []string{"rbind", "rw"}}}
	}
	return sp
}

func listed(d *daemon.Daemon, id string) bool {
	for _, x := range d.Sup.List() {
		if x == id {
			return true
		}
	}
	return false
}

func logCount(t *testing.T, d *daemon.Daemon, id, s string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(d.Base, "logs", id+".log"))
	return strings.Count(string(b), s)
}

func daemonLogHas(d *daemon.Daemon, s string) bool { return strings.Contains(d.Log.String(), s) }

func TestRestartPolicies(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()

	// on-failure: the first run fails after running; the restart exits 0 at
	// once (before "running" is observed) and is not restarted again.
	st1 := t.TempDir()
	if err := d.Sup.Start(ctx, spec(t, d, "onfail", supervisor.RestartOnFailure, st1,
		"sh", "-c", "cat /state/m && exit 0; echo first; touch /state/m; sleep 0.5; exit 1")); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 20*time.Second, "onfail to finish", func() bool { return !listed(d, "onfail") })
	if n := logCount(t, d, "onfail", "first"); n != 1 {
		t.Errorf("first run printed %d times", n)
	}

	// always + MaxRestarts: fast exits restart until the limit.
	st2 := t.TempDir()
	sp := spec(t, d, "always", supervisor.RestartAlways, st2,
		"sh", "-c", "cat /state/m && exit 0; touch /state/m; sleep 0.5; exit 1")
	sp.MaxRestarts = 3
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 30*time.Second, "always to hit MaxRestarts", func() bool { return !listed(d, "always") })

	// on-failure that keeps failing after running.
	sp = spec(t, d, "flaky", supervisor.RestartOnFailure, "", "sh", "-c", "echo try; sleep 0.3; exit 2")
	sp.MaxRestarts = 2
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 30*time.Second, "flaky to give up", func() bool { return !listed(d, "flaky") })
	if n := logCount(t, d, "flaky", "try"); n != 3 {
		t.Errorf("flaky ran %d times, want 3", n)
	}

	// never: one run.
	if err := d.Sup.Start(ctx, spec(t, d, "once", supervisor.RestartNever, "", "sh", "-c", "sleep 0.3; exit 5")); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 20*time.Second, "once to end", func() bool { return !listed(d, "once") })

	// StartSequential stops at the first failure.
	err := d.Sup.StartSequential(ctx, []supervisor.ContainerSpec{
		spec(t, d, "seq", supervisor.RestartNever, "", "sleep", "60"),
		spec(t, d, "seq", supervisor.RestartNever, "", "sleep", "60"),
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("StartSequential duplicate: %v", err)
	}
	if d.Sup.BaseDir() != d.Base {
		t.Error("BaseDir")
	}
	if err := d.Sup.Remove(ctx, "seq"); err != nil {
		t.Error(err)
	}
}

func TestRestartAfterStartFailure(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.Start(ctx, spec(t, d, "r", supervisor.RestartAlways, state, "sh", "-c", "echo up; sleep 0.5; exit 1")); err != nil {
		t.Fatal(err)
	}
	// The bind source disappears: restarts fail until it is back.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 20*time.Second, "a failed restart", func() bool { return daemonLogHas(d, "restart failed") })
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 30*time.Second, "a restart after recovery", func() bool { return logCount(t, d, "r", "up") >= 2 })
	if err := d.Sup.Kill(ctx, "r", "KILL"); err != nil {
		t.Fatal(err)
	}
}

func TestHealthDrivenRestart(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	state := t.TempDir()
	healthy := filepath.Join(state, "healthy")
	if err := os.WriteFile(healthy, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sp := spec(t, d, "hc", supervisor.RestartUnlessStopped, state, "sh", "-c", "echo boot; sleep 300")
	sp.Healthcheck = &health.Config{Type: health.TypeExec, Command: []string{"cat", "/state/healthy"},
		Interval: 100 * time.Millisecond, Timeout: 2 * time.Second, Retries: 1}
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		if err := os.Remove(healthy); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for logCount(t, d, "hc", "boot") <= round {
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(filepath.Join(d.Base, "logs", "hc.log"))
				t.Fatalf("round %d: no health-driven restart; container log:\n%s", round, b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := os.WriteFile(healthy, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		// Healthy again before the next round.
		time.Sleep(500 * time.Millisecond)
	}
	if err := d.Sup.Stop(ctx, "hc"); err != nil {
		t.Fatal(err)
	}

	// Policy never: unhealthy is only logged.
	sp = spec(t, d, "hc-never", supervisor.RestartNever, state, "sleep", "300")
	sp.Healthcheck = &health.Config{Type: health.TypeExec, Command: []string{"cat", "/state/healthy"},
		Interval: 100 * time.Millisecond, Timeout: 2 * time.Second, Retries: 1}
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(healthy)
	daemon.WaitFor(t, 20*time.Second, "unhealthy log", func() bool { return daemonLogHas(d, "restart policy does not restart") })
	if !listed(d, "hc-never") {
		t.Error("unhealthy container with policy never was stopped")
	}

	// Readiness never succeeds: the start fails and nothing is left behind.
	sp = spec(t, d, "notready", supervisor.RestartAlways, "", "sleep", "300")
	sp.Healthcheck = &health.Config{Type: health.TypeExec, Command: []string{"false"}, Interval: 100 * time.Millisecond, Timeout: time.Second}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.Sup.Start(sctx, sp); err == nil || !strings.Contains(err.Error(), "readiness") {
		t.Fatalf("readiness failure: %v", err)
	}
	if listed(d, "notready") {
		t.Error("container kept after readiness failure")
	}
}

// A second supervisor on the same state (a restarted daemon) re-adopts
// running containers from its records, or from crun + bundle metadata.
func TestReadoptAfterDaemonRestart(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	for i, id := range []string{"keep", "orphan"} {
		// Policy never: the first supervisor stands for a daemon that is gone;
		// it must not restart what the second one stops.
		sp := spec(t, d, id, supervisor.RestartNever, "", "sleep", "300")
		sp.PortMappings = []network.PortMapping{{HostPort: 18001 + 10*i, ContainerPort: 18002 + 10*i}}
		if err := d.Sup.Start(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	stateDir := filepath.Join(d.Base, "supervisor", "containers")
	// "orphan" lost its record; stale and broken records are ignored or removed.
	if err := os.Remove(filepath.Join(stateDir, "orphan.json")); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"corrupt.json":  "{",
		"oldver.json":   `{"v":99,"spec":{"ID":"oldver"}}`,
		"mismatch.json": `{"v":1,"spec":{"ID":"other"}}`,
		"gone.json":     `{"v":1,"spec":{"ID":"gone"}}`,
		"notes.txt":     "x",
	} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(stateDir, "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup2 := supervisor.New(d.Runtime, d.Overlay, network.NewHostBackend(), d.Base, log, d.Logs, d.Store, nil, "host", "auto", "")
	sup2.SetRootless(true, false)
	got := strings.Join(sup2.List(), ",")
	if !strings.Contains(got, "keep") || !strings.Contains(got, "orphan") {
		t.Fatalf("re-adopted: %s", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "gone.json")); !os.IsNotExist(err) {
		t.Error("record of a container crun does not know was kept")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "orphan.json")); err != nil {
		t.Error("orphan was not persisted again")
	}
	if infos := sup2.ListInfo(ctx); len(infos) != 2 {
		t.Errorf("ListInfo %+v", infos)
	}
	// Stop through the new supervisor: no foreground run to wait for.
	if err := sup2.Stop(ctx, "keep"); err != nil {
		t.Fatal(err)
	}
	if err := sup2.Kill(ctx, "orphan", "KILL"); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 30*time.Second, "re-adopted containers to end", func() bool { return len(sup2.List()) == 0 })
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sup2.Shutdown(sctx)
}

func TestNativeNetworkAndDNS(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/nft"); err != nil {
		t.Skip("nft not installed")
	}
	if !nsrun.Run(t, "/run") {
		return
	}
	d := daemon.StartWith(t, daemon.Options{NetDriver: "native"})
	d.Image(t, "test/app:1", nil)
	ctx := context.Background()
	web := spec(t, d, "web", supervisor.RestartNever, "", "sleep", "300")
	web.Hostname = "Web.Box"
	web.PortMappings = []network.PortMapping{{HostPort: 18080, ContainerPort: 80}}
	if err := d.Sup.Start(ctx, web); err != nil {
		t.Fatal(err)
	}
	client := spec(t, d, "client", supervisor.RestartNever, "", "sh", "-c", "cat /etc/resolv.conf; lookup web.box.nyxd.local; lookup web.box")
	if err := d.Sup.Start(ctx, client); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 20*time.Second, "client lookups", func() bool { return logCount(t, d, "client", "10.88.0.") >= 2 })
	if logCount(t, d, "client", "nameserver 10.88.0.1") != 1 {
		b, _ := os.ReadFile(filepath.Join(d.Base, "logs", "client.log"))
		t.Errorf("resolv.conf in container:\n%s", b)
	}
	var ip string
	for _, in := range d.Sup.ListInfo(ctx) {
		if in.ID == "web" {
			ip = in.IP
			if in.Ports == "" {
				t.Error("ports not listed")
			}
		}
	}
	if !strings.HasPrefix(ip, "10.88.0.") {
		t.Fatalf("web ip %q", ip)
	}
	if err := d.Sup.Remove(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/run/nyxd/netns/web"); !os.IsNotExist(err) {
		t.Error("netns left behind")
	}
}

func TestNewWithoutDNS(t *testing.T) {
	// nil DNS backend defaults to no-op; nothing to reconcile in an empty dir.
	s := supervisor.New(nil, nil, network.NewHostBackend(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, "host", "", "")
	if len(s.List()) != 0 || len(s.ImageRefsInUse()) != 0 {
		t.Fatal("new supervisor not empty")
	}
	s.Shutdown(context.Background())
}

func TestStopPathsAndStartFailures(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()

	// TERM ignored: KILL after the grace period.
	sp := spec(t, d, "stubborn", supervisor.RestartAlways, "", "ignoreterm")
	sp.StopTimeout = 300 * time.Millisecond
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := d.Sup.Stop(ctx, "stubborn"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("stop took %s", time.Since(start))
	}

	// The image's STOPSIGNAL is used.
	d.Image(t, "test/stopsig:1", func(c *oci.ImageConfig) { c.Config.StopSignal = "INT" })
	m, cfg, paths, err := d.Store.ResolvePulledImage("test/stopsig:1")
	if err != nil {
		t.Fatal(err)
	}
	sig := supervisor.ContainerSpec{ID: "sig", Image: "test/stopsig:1", ImageConfig: cfg, ManifestLayers: m.Layers, BlobPaths: paths, Args: []string{"trapterm"}}
	if err := d.Sup.Start(ctx, sig); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.Stop(ctx, "sig"); err != nil {
		t.Fatal(err)
	}
	if !daemonLogHas(d, `"signal":"INT"`) {
		t.Error("STOPSIGNAL not used")
	}

	// Each start step can fail; nothing is left registered.
	badUser := *cfg
	badUser.Config.User = "nosuchuser"
	for name, sp := range map[string]supervisor.ContainerSpec{
		"overlay": {ID: "f1", Image: "x", ImageConfig: cfg, BlobPaths: []string{"/nonexistent/blob"}},
		"user":    {ID: "f2", Image: "x", ImageConfig: &badUser, ManifestLayers: m.Layers, BlobPaths: paths},
		"crun":    {ID: "f3", Image: "x", ImageConfig: cfg, ManifestLayers: m.Layers, BlobPaths: paths, Args: []string{"/nonexistent"}},
		"bundle":  {ID: "f4", Image: "x", ImageConfig: cfg, ManifestLayers: m.Layers, BlobPaths: paths, SeccompProfile: "/nonexistent/profile.json"},
	} {
		if err := d.Sup.Start(ctx, sp); err == nil {
			t.Errorf("%s: start succeeded", name)
		}
		if listed(d, sp.ID) {
			t.Errorf("%s: failed container still listed", name)
		}
	}

	// compose up: unchanged spec is kept, a changed one is recreated.
	a := spec(t, d, "svc", supervisor.RestartUnlessStopped, "", "sh", "-c", "echo v1; sleep 300")
	a.Healthcheck = &health.Config{Type: health.TypeNone}
	if err := d.Sup.StartComposeUp(ctx, []supervisor.ContainerSpec{a}); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.StartComposeUp(ctx, []supervisor.ContainerSpec{a}); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Args = []string{"sh", "-c", "echo v2; sleep 300"}
	if err := d.Sup.StartComposeUp(ctx, []supervisor.ContainerSpec{b}); err != nil {
		t.Fatal(err)
	}
	waitLogLine := func(s string) {
		daemon.WaitFor(t, 15*time.Second, s, func() bool { return logCount(t, d, "svc", s) > 0 })
	}
	waitLogLine("v2")
	if logCount(t, d, "svc", "v1") != 1 {
		t.Error("unchanged spec was recreated")
	}
}

// failNet fails Setup (and logs Teardown) for the native-path start failure.
type failNet struct{}

func (failNet) EnsureNetwork() error { return nil }
func (failNet) Setup(context.Context, string, string, []network.PortMapping, *network.SetupOptions) (string, error) {
	return "", errors.New("no addresses left")
}
func (failNet) Teardown(context.Context, string, string) error { return errors.New("teardown failed") }

func TestNetworkSetupFailure(t *testing.T) {
	if !nsrun.Run(t, "/run") {
		return
	}
	d := setup(t)
	sup := supervisor.New(d.Runtime, d.Overlay, failNet{}, d.Base, slog.New(slog.NewTextHandler(io.Discard, nil)), d.Logs, d.Store, nil, "native", "off", "")
	sup.SetRootless(true, false)
	if err := sup.Start(context.Background(), spec(t, d, "n", supervisor.RestartNever, "", "true")); err == nil || !strings.Contains(err.Error(), "network setup") {
		t.Fatalf("start with failing network: %v", err)
	}
	if _, err := os.Stat("/run/nyxd/netns/n"); !os.IsNotExist(err) {
		t.Error("netns left behind")
	}
}

func TestShutdownAndForcedPaths(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// No log collector; resource limits dropped without cgroups; compose
	// internal network options passed to the backend.
	plain := supervisor.New(d.Runtime, d.Overlay, network.NewHostBackend(), d.Base, log, nil, d.Store, nil, "host", "auto", "")
	plain.SetRootless(true, false)
	sp := spec(t, d, "nolog", supervisor.RestartNever, "", "sh", "-c", "echo x; sleep 300")
	sp.Resources = &bundle.Resources{}
	sp.ComposeInternalNet = true
	if err := plain.Start(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if err := plain.Remove(context.Background(), "nolog"); err != nil {
		t.Fatal(err)
	}

	// The daemon context ends while a container runs, and while a restart waits.
	ctx, cancel := context.WithCancel(context.Background())
	if err := plain.Start(ctx, spec(t, d, "running", supervisor.RestartNever, "", "sleep", "300")); err != nil {
		t.Fatal(err)
	}
	if err := plain.Start(ctx, spec(t, d, "looping", supervisor.RestartAlways, "", "sh", "-c", "sleep 0.3; exit 1")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond) // first exit, then the restart backoff
	cancel()
	time.Sleep(300 * time.Millisecond)
	for _, id := range []string{"running", "looping"} {
		_ = d.Runtime.Delete(context.Background(), id, true)
	}

	// A re-adopted container that ignores SIGTERM: grace period, then a forced delete.
	stub := spec(t, d, "stubborn2", supervisor.RestartNever, "", "ignoreterm")
	stub.StopTimeout = 300 * time.Millisecond
	if err := d.Sup.Start(context.Background(), stub); err != nil {
		t.Fatal(err)
	}
	sup2 := supervisor.New(d.Runtime, d.Overlay, network.NewHostBackend(), d.Base, log, d.Logs, d.Store, nil, "host", "auto", "")
	sup2.SetRootless(true, false)
	if err := sup2.Stop(context.Background(), "stubborn2"); err != nil {
		t.Fatalf("forced stop: %v", err)
	}
	if err := sup2.KillForRestart(context.Background(), "stubborn2"); err == nil {
		t.Log("container still tracked after forced stop") // either way is fine
	}
}

func roMount(t *testing.T, dir string) {
	t.Helper()
	_ = os.MkdirAll(dir, 0o700)
	if err := unix.Mount(dir, dir, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("", dir, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Unmount(dir, unix.MNT_DETACH) })
}

func TestReadOnlyState(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	// Records cannot be written: the container still runs (logged).
	roMount(t, filepath.Join(d.Base, "supervisor", "containers"))
	if err := d.Sup.Start(ctx, spec(t, d, "norecord", supervisor.RestartNever, "", "sleep", "300")); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.Remove(ctx, "norecord"); err != nil {
		t.Fatal(err)
	}
	// Bundles cannot be created: the start fails and is rolled back.
	roMount(t, filepath.Join(d.Base, "bundles"))
	if err := d.Sup.Start(ctx, spec(t, d, "nobundle", supervisor.RestartNever, "", "true")); err == nil {
		t.Error("start without a bundle dir")
	}
	if listed(d, "nobundle") {
		t.Error("failed start still listed")
	}
}
