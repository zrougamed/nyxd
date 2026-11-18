package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// UpdateLabel opts a container into automatic image updates: "digest"
// (update when the tag's digest changes) or "off" (the default).
const UpdateLabel = "nyx.update"

// UpdateCandidates returns the specs of running containers labelled
// nyx.update=digest, sorted by id.
func (s *Supervisor) UpdateCandidates() []ContainerSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ContainerSpec
	for _, e := range s.containers {
		if strings.EqualFold(strings.TrimSpace(e.spec.Labels[UpdateLabel]), "digest") {
			out = append(out, e.spec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ErrRolledBack wraps the reason a replaced container was put back.
var ErrRolledBack = errors.New("rolled back")

// ReplaceImage recreates the running container want.ID from want (the same
// spec with a new image). If the new container does not start, or does not
// pass its health check (Start waits for readiness when the spec has one),
// the old spec is started again and the error wraps ErrRolledBack.
func (s *Supervisor) ReplaceImage(ctx context.Context, want ContainerSpec) error {
	s.mu.RLock()
	e, ok := s.containers[want.ID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("container %s is not running", want.ID)
	}
	e.mu.Lock()
	old := e.spec
	e.mu.Unlock()
	if err := s.Stop(ctx, want.ID); err != nil {
		return fmt.Errorf("stop for update: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := s.waitContainerAbsent(waitCtx, want.ID); err != nil {
		return fmt.Errorf("stop for update: %w", err)
	}
	startErr := s.Start(ctx, want)
	if startErr == nil {
		return nil
	}
	s.log.Warn("update failed, putting the previous image back", "id", want.ID, "err", startErr)
	if s.Running(want.ID) { // started but failed later: make room for the old one
		_ = s.Stop(ctx, want.ID)
		_ = s.waitContainerAbsent(waitCtx, want.ID)
	}
	if err := s.Start(ctx, old); err != nil {
		return fmt.Errorf("%w: %v; restarting the previous image also failed: %v", ErrRolledBack, startErr, err)
	}
	return fmt.Errorf("%w: %v", ErrRolledBack, startErr)
}
