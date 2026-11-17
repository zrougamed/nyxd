package supervisor

import (
	"context"
	"sort"

	"github.com/zrougamed/nyxd/internal/cgstats"
)

// ContainerMetrics is one supervised container's counters for GET
// /v1/metrics: cumulative cgroup values read once, no sampling interval.
type ContainerMetrics struct {
	ID       string
	Image    string
	Project  string // compose project, empty for nyx run
	Service  string // compose service
	Restarts int
	// Health is healthy, unhealthy or starting; empty without a health check.
	Health string
	Sample cgstats.Sample
	// HasSample is false when the container's process could not be read
	// (between restarts, or just exited).
	HasSample bool
}

// Metrics returns counters for every supervised container, sorted by id.
func (s *Supervisor) Metrics(ctx context.Context) []ContainerMetrics {
	s.mu.RLock()
	entries := make([]*containerEntry, 0, len(s.containers))
	for _, e := range s.containers {
		entries = append(entries, e)
	}
	s.mu.RUnlock()

	out := make([]ContainerMetrics, 0, len(entries))
	for _, e := range entries {
		e.mu.Lock()
		m := ContainerMetrics{
			ID:       e.spec.ID,
			Image:    e.spec.Image,
			Project:  e.spec.ComposeProject,
			Service:  e.spec.ComposeService,
			Restarts: e.restarts,
		}
		if e.checker != nil {
			m.Health = string(e.checker.Status())
		}
		e.mu.Unlock()
		if s.rt != nil {
			if pid, _ := s.runtimePID(ctx, m.ID); pid > 0 {
				if smp, err := s.readSample(pid); err == nil {
					m.Sample, m.HasSample = smp, true
				}
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
