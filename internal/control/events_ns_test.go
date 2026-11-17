//go:build linux

package control_test

import (
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlEvents(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	_, sub := d.Events.Subscribe(time.Time{}, nil)
	defer sub.Close()
	seen := map[string][]events.Event{}
	collect := func(until time.Duration, want func() bool) {
		t.Helper()
		deadline := time.After(until)
		for !want() {
			select {
			case e := <-sub.C:
				seen[e.ID+" "+e.Action] = append(seen[e.ID+" "+e.Action], e)
			case <-deadline:
				t.Fatalf("timed out; got %v", keys(seen))
			}
		}
	}
	has := func(k string) bool { return len(seen[k]) > 0 }

	// A workload that exits during start.
	d.Do(t, "POST", "/v1/containers/run", map[string]any{"id": "quick", "image": "test/app:1", "args": []string{"sh", "-c", "exit 3"}, "restart": "no"})
	collect(10*time.Second, func() bool { return has("quick create") && has("quick die") })
	if a := seen["quick die"][0].Attrs; a["exit_code"] != "3" {
		t.Errorf("quick die: %v", a)
	}

	// Restarts on failure.
	run(t, d, map[string]any{"id": "flaky", "args": []string{"sh", "-c", "sleep 1; exit 2"}, "restart": "on-failure"})
	collect(30*time.Second, func() bool { return len(seen["flaky restart"]) >= 1 && len(seen["flaky die"]) >= 2 })
	if seen["flaky restart"][0].Attrs["attempt"] != "1" || seen["flaky die"][0].Attrs["exit_code"] != "2" {
		t.Errorf("flaky: %v / %v", seen["flaky restart"][0].Attrs, seen["flaky die"][0].Attrs)
	}

	// A compose service with a health check reports health_status.
	dir := t.TempDir()
	yaml := `services:
  hc:
    image: test/app:1
    command: [sleep, "300"]
    healthcheck: {test: ["CMD", "true"], interval: 1s}
`
	if code, out := d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": yaml, "context_dir": dir, "project": "ev"}); code != 200 {
		t.Fatalf("compose up: %d %s", code, out)
	}
	collect(30*time.Second, func() bool { return has("ev-hc health_status") })
	if e := seen["ev-hc health_status"][0]; e.Attrs["status"] != "healthy" || e.Attrs["compose_service"] != "hc" {
		t.Errorf("health: %+v", e)
	}
	d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": yaml, "context_dir": dir, "project": "ev"})
	collect(20*time.Second, func() bool { return has("ev-hc remove") })
	d.Do(t, "POST", "/v1/containers/flaky/remove", nil)
}

func keys(m map[string][]events.Event) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
