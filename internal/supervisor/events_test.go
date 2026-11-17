package supervisor

import (
	"os/exec"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
)

func TestSupervisorEventAttributes(t *testing.T) {
	bus := events.New(20)
	s := &Supervisor{log: quiet}
	s.emit(events.Start, ContainerSpec{ID: "x"}, nil) // no bus: nothing, no panic
	s.SetEvents(bus)
	spec := ContainerSpec{ID: "shop-db", Image: "postgres:17", ComposeProject: "shop", ComposeService: "db"}

	s.emit(events.Start, spec, nil)
	s.emitDie(&containerEntry{spec: spec}, 143, true)
	s.emitDie(&containerEntry{spec: spec, cgroup: t.TempDir()}, 137, false) // no oom_kill recorded: no oom event
	s.emitDie(&containerEntry{spec: spec}, 3, false)
	err := exec.Command("sh", "-c", "exit 7").Run()
	s.emitStartFailed(spec, err)
	s.emitStartFailed(spec, errTest("bundle setup failed"))

	past, sub := bus.Subscribe(time.Unix(0, 0), nil)
	sub.Close()
	if len(past) != 6 {
		t.Fatalf("events: %+v", past)
	}
	if e := past[0]; e.Action != events.Start || e.ID != "shop-db" || e.Image != "postgres:17" || e.Attrs["compose_project"] != "shop" || e.Attrs["compose_service"] != "db" {
		t.Errorf("start: %+v", e)
	}
	if a := past[1].Attrs; a["exit_code"] != "143" || a["signal"] != "15" || a["stopped"] != "true" {
		t.Errorf("stopped die: %v", a)
	}
	if a := past[2].Attrs; past[2].Action != events.Die || a["signal"] != "9" || a["stopped"] != "" {
		t.Errorf("killed die: %+v", past[2])
	}
	if a := past[3].Attrs; a["exit_code"] != "3" || a["signal"] != "" {
		t.Errorf("exit 3: %v", a)
	}
	if a := past[4].Attrs; a["exit_code"] != "7" || a["error"] == "" {
		t.Errorf("start failed with exit: %v", a)
	}
	if a := past[5].Attrs; a["exit_code"] != "" || a["error"] != "bundle setup failed" {
		t.Errorf("start failed: %v", a)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
