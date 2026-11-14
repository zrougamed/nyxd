package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Containers that exited for good keep their spec under
// {baseDir}/supervisor/exited/{id}.json until they are removed, so nyx start
// can bring them back and nyx ps -a can list them. --rm containers leave no
// record.

const exitedRecordVersion = 1

// waitFinishedTimeout bounds how long Restart waits for the monitor to tear a
// stopped container down (network, overlay, bundle) before starting it again.
const waitFinishedTimeout = 30 * time.Second

type exitedRecord struct {
	Version    int           `json:"v"`
	Spec       ContainerSpec `json:"spec"`
	ExitCode   int           `json:"exit_code"`
	FinishedAt time.Time     `json:"finished_at"`
}

// ErrNoContainer is returned when an id names neither a running nor an
// exited container.
var ErrNoContainer = errors.New("no such container")

func (s *Supervisor) exitedDir() string {
	return filepath.Join(s.baseDir, "supervisor", "exited")
}

func (s *Supervisor) exitedPath(id string) string {
	return filepath.Join(s.exitedDir(), id+".json")
}

// recordExited saves the spec of a container that will not run again unless
// started. Failures are logged: losing the record only loses nyx start.
func (s *Supervisor) recordExited(spec ContainerSpec, exitCode int) {
	if spec.AutoRemove || spec.ID == "" {
		return
	}
	rec := exitedRecord{Version: exitedRecordVersion, Spec: spec, ExitCode: exitCode, FinishedAt: time.Now().UTC()}
	if err := writeJSONAtomic(s.exitedDir(), s.exitedPath(spec.ID), rec); err != nil {
		s.log.Warn("record exited container", "id", spec.ID, "err", err)
	}
}

func (s *Supervisor) readExited(id string) (exitedRecord, bool) {
	var rec exitedRecord
	b, err := os.ReadFile(s.exitedPath(id))
	if err != nil {
		return rec, false
	}
	if err := json.Unmarshal(b, &rec); err != nil || rec.Spec.ID != id {
		s.log.Warn("exited container record unreadable", "id", id, "err", err)
		return rec, false
	}
	return rec, true
}

func (s *Supervisor) forgetExited(id string) {
	if id != "" {
		_ = os.Remove(s.exitedPath(id))
	}
}

// ExitedIDs returns the ids of exited containers that have not been removed.
func (s *Supervisor) ExitedIDs() []string {
	entries, err := os.ReadDir(s.exitedDir())
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// ExitedInfo lists exited containers in the shape of [Supervisor.ListInfo].
func (s *Supervisor) ExitedInfo() []ContainerInfo {
	var out []ContainerInfo
	for _, id := range s.ExitedIDs() {
		if s.Running(id) {
			continue
		}
		rec, ok := s.readExited(id)
		if !ok {
			continue
		}
		code := rec.ExitCode
		out = append(out, ContainerInfo{
			ID:          id,
			ShortID:     DisplayID(id),
			Image:       rec.Spec.Image,
			Ports:       formatPortMappings(rec.Spec.PortMappings),
			Status:      "exited",
			ExitCode:    &code,
			HostNetwork: rec.Spec.HostNetwork || s.hostNetwork(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Running reports whether id is a supervised (running or restarting) container.
func (s *Supervisor) Running(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.containers[id]
	return ok
}

// StartExisting starts an exited container again from its saved spec.
func (s *Supervisor) StartExisting(ctx context.Context, id string) error {
	if s.Running(id) {
		return fmt.Errorf("container %s is already running", id)
	}
	rec, ok := s.readExited(id)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoContainer, id)
	}
	spec, err := s.refreshImage(rec.Spec)
	if err != nil {
		return err
	}
	return s.Start(ctx, spec)
}

// refreshImage keeps the image a container was created with while its layers
// are still in the store, like docker start. If they were pruned (the tag was
// pulled again since), the current image for the reference is used.
func (s *Supervisor) refreshImage(spec ContainerSpec) (ContainerSpec, error) {
	present := len(spec.BlobPaths) > 0
	for _, p := range spec.BlobPaths {
		if _, err := os.Stat(p); err != nil {
			present = false
			break
		}
	}
	if present || s.imgStore == nil {
		return spec, nil
	}
	m, cfg, paths, err := s.imgStore.ResolvePulledImage(spec.Image)
	if err != nil {
		return spec, fmt.Errorf("image %s of container %s is no longer in the store (nyx pull %s): %w", spec.Image, spec.ID, spec.Image, err)
	}
	s.log.Info("container image layers were pruned, using the current image", "id", spec.ID, "image", spec.Image)
	spec.ImageConfig, spec.ManifestLayers, spec.BlobPaths = cfg, m.Layers, paths
	return spec, nil
}

// Restart stops a running container (grace overrides its stop timeout when
// positive) and starts it again with the same spec. An exited container is
// just started.
func (s *Supervisor) Restart(ctx context.Context, id string, grace time.Duration) error {
	s.mu.RLock()
	entry, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		return s.StartExisting(ctx, id)
	}
	entry.mu.Lock()
	spec := entry.spec
	entry.mu.Unlock()
	finished := entry.finishedChan()

	if err := s.stop(ctx, id, grace); err != nil {
		return err
	}
	if !waitClosed(finished, waitFinishedTimeout) {
		return fmt.Errorf("container %s: still tearing down after %s", id, waitFinishedTimeout)
	}
	return s.Start(ctx, spec)
}

// finishedChan returns a channel closed once the container's monitor has
// returned (teardown done, entry dropped). Safe to call before or after.
func (e *containerEntry) finishedChan() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished == nil {
		e.finished = make(chan struct{})
		if e.monitorDone {
			close(e.finished)
		}
	}
	return e.finished
}

// markFinished is deferred by supervise.
func (e *containerEntry) markFinished() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitorDone = true
	if e.finished != nil {
		select {
		case <-e.finished:
		default:
			close(e.finished)
		}
	}
}
