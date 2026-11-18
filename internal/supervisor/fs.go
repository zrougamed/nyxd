package supervisor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// ErrNotRunning is returned for a container that exists but is not running
// when the operation needs its filesystem.
var ErrNotRunning = errors.New("container is not running")

// ContainerFS returns the root filesystem (the overlay's merged directory,
// as the daemon sees it) and the extra mounts of a running container, for
// nyx cp. An exited container's overlay is gone: ErrNotRunning.
func (s *Supervisor) ContainerFS(id string) (string, []bundle.Mount, error) {
	s.mu.RLock()
	e, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		if _, exited := s.readExited(id); exited {
			return "", nil, fmt.Errorf("%w: %s (its filesystem exists only while it runs)", ErrNotRunning, id)
		}
		return "", nil, ErrNoContainer
	}
	e.mu.Lock()
	rootFS := e.rootFS
	mounts := append([]bundle.Mount(nil), e.spec.ExtraMounts...)
	e.mu.Unlock()
	if rootFS == "" && s.ovl != nil && s.ovl.IsMounted(id) {
		rootFS = s.ovl.MergedDir(id) // re-adopted after a daemon restart
	}
	if rootFS == "" {
		return "", nil, fmt.Errorf("%w: %s (no root filesystem mounted)", ErrNotRunning, id)
	}
	return rootFS, mounts, nil
}

// CheckContainerID rejects ids nyxd keeps for itself: a name starting with
// "_" could be the directory of the overlay's shared layer store (_layers)
// or its legacy cache, which sit next to each container's overlay.
func CheckContainerID(id string) error {
	if strings.HasPrefix(id, "_") {
		return fmt.Errorf("invalid container name %q: names starting with '_' are reserved for nyxd", id)
	}
	return nil
}
