package supervisor

import (
	"errors"
	"os/exec"
	"strconv"

	"github.com/zrougamed/nyxd/internal/cgstats"
	"github.com/zrougamed/nyxd/internal/events"
	"github.com/zrougamed/nyxd/internal/runtime"
)

// SetEvents sets the bus container events go to (nil: none).
func (s *Supervisor) SetEvents(b *events.Bus) { s.events = b }

// emit publishes a container event for spec. It never blocks.
func (s *Supervisor) emit(action string, spec ContainerSpec, attrs map[string]string) {
	if s.events == nil {
		return
	}
	if spec.ComposeProject != "" {
		if attrs == nil {
			attrs = map[string]string{}
		}
		attrs["compose_project"], attrs["compose_service"] = spec.ComposeProject, spec.ComposeService
	}
	s.events.Publish(events.Event{Type: events.TypeContainer, Action: action, ID: spec.ID, Image: spec.Image, Attrs: attrs})
}

// emitDie publishes die (exit code, signal for 128+N, whether nyx stopped
// it) and, when the kernel's OOM killer ended it, oom first.
func (s *Supervisor) emitDie(e *containerEntry, exitCode int, intentional bool) {
	e.mu.Lock()
	spec, cg := e.spec, e.cgroup
	e.mu.Unlock()
	attrs := map[string]string{"exit_code": strconv.Itoa(exitCode)}
	if exitCode > 128 {
		attrs["signal"] = strconv.Itoa(exitCode - 128)
	}
	if intentional {
		attrs["stopped"] = "true"
	}
	if exitCode == 137 && !intentional && cgstats.OOMKills(cg) > 0 {
		s.emit(events.OOM, spec, nil)
	}
	s.emit(events.Die, spec, attrs)
}

// emitStartFailed publishes die for a container that never got running:
// its workload exited during start (with its exit code) or setup failed.
func (s *Supervisor) emitStartFailed(spec ContainerSpec, err error) {
	attrs := map[string]string{"error": err.Error()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		attrs["exit_code"] = strconv.Itoa(runtime.ExitCode(err))
	}
	s.emit(events.Die, spec, attrs)
}
