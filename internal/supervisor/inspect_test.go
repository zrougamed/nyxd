package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/cgstats"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/runtime"
)

func TestStatsTargetsEmpty(t *testing.T) {
	s := &Supervisor{containers: map[string]*containerEntry{}}
	ids, err := s.statsTargets(nil)
	if err != nil || len(ids) != 0 {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestStatsTargetsMissing(t *testing.T) {
	s := &Supervisor{containers: map[string]*containerEntry{
		"a": {spec: ContainerSpec{ID: "a"}},
	}}
	if _, err := s.statsTargets([]string{"missing"}); err == nil {
		t.Fatal("expected error")
	}
	ids, err := s.statsTargets([]string{"a", "a"})
	if err != nil || len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestCPUPercentWiring(t *testing.T) {
	prev := cgstats.Sample{CPUUsageUsec: 0, Timestamp: time.Unix(0, 0)}
	cur := cgstats.Sample{CPUUsageUsec: 1_000_000, Timestamp: time.Unix(1, 0)}
	if p := cgstats.CPUPercent(prev, cur); p < 99 || p > 101 {
		t.Fatalf("%v", p)
	}
}

func testSupervisorWithEntry(t *testing.T, id string) *Supervisor {
	t.Helper()
	base := t.TempDir()
	rt, err := runtime.New("crun", filepath.Join(base, "run", "crun"))
	if err != nil {
		t.Skip(err)
	}
	s := &Supervisor{
		rt:         rt,
		baseDir:    base,
		containers: map[string]*containerEntry{},
		log:        quiet,
	}
	e := &containerEntry{
		spec: ContainerSpec{
			ID:            id,
			Image:         "test/app:1",
			Env:           []string{"A=1"},
			Args:          []string{"sleep", "1"},
			Hostname:      "box",
			RestartPolicy: RestartNever,
			PortMappings:  []network.PortMapping{{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}},
			HostNetwork:   true,
		},
		ip:        "10.88.0.9",
		bundleDir: filepath.Join(base, "bundles", id),
		rootFS:    filepath.Join(base, "rootfs", id),
		netNS:     "",
		restarts:  2,
	}
	_ = os.MkdirAll(filepath.Dir(s.persistPath(id)), 0o700)
	_ = s.persistContainerEntry(e)
	s.containers[id] = e
	return s
}

func TestInspectNotFound(t *testing.T) {
	s := testSupervisorWithEntry(t, "web")
	if _, err := s.Inspect(context.Background(), "nosuch"); err == nil {
		t.Fatal("expected error")
	}
}

func TestInspectFields(t *testing.T) {
	s := testSupervisorWithEntry(t, "web")
	info, err := s.Inspect(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "web" || info.Image != "test/app:1" || info.IP != "10.88.0.9" {
		t.Fatalf("%+v", info)
	}
	if info.Restarts != 2 || info.Hostname != "box" || !info.HostNetwork {
		t.Fatalf("%+v", info)
	}
	if info.ShortID == "" || info.CreatedAt == "" {
		t.Fatalf("short/created missing: %+v", info)
	}
	if info.PortsDisplay == "" {
		t.Fatal("ports_display")
	}
	// crun has no such container → absent or unknown
	if info.Status == "" {
		t.Fatal("status")
	}
}

func TestStatsEmptyAndNotRunning(t *testing.T) {
	s := &Supervisor{containers: map[string]*containerEntry{}, log: quiet}
	items, err := s.Stats(context.Background(), nil, 10*time.Millisecond)
	if err != nil || len(items) != 0 {
		t.Fatalf("%v %v", items, err)
	}

	s = testSupervisorWithEntry(t, "web")
	items, err = s.Stats(context.Background(), []string{"web"}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "web" {
		t.Fatalf("%+v", items)
	}
	// Not running under crun → error field set, zeros elsewhere.
	if items[0].Err == "" {
		t.Fatalf("expected not-running error, got %+v", items[0])
	}
}

func TestStatsMissingID(t *testing.T) {
	s := testSupervisorWithEntry(t, "web")
	if _, err := s.Stats(context.Background(), []string{"gone"}, time.Millisecond); err == nil {
		t.Fatal("expected error")
	}
}

func TestRuntimePIDAbsent(t *testing.T) {
	s := testSupervisorWithEntry(t, "web")
	pid, status := s.runtimePID(context.Background(), "web")
	if pid != 0 {
		t.Fatalf("pid %d status %s", pid, status)
	}
	if status != "absent" && status != "unknown" {
		t.Fatalf("status %q", status)
	}
}
