package supervisor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// VolumeInUse reports whether a container, running or exited, mounts dir or
// a path under it. Exited containers count: nyx start would bring them back
// without their data.
func (s *Supervisor) VolumeInUse(dir string) bool {
	if s.IsBindSourceInUse(dir) {
		return true
	}
	dir = filepath.Clean(dir)
	for _, id := range s.ExitedIDs() {
		rec, ok := s.readExited(id)
		if !ok {
			continue
		}
		for _, m := range rec.Spec.ExtraMounts {
			src := filepath.Clean(m.Source)
			if src == dir || strings.HasPrefix(src, dir+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// OverlayDir is the directory holding a container's overlay (upper, work and
// merged directories).
func (s *Supervisor) OverlayDir(id string) string {
	return filepath.Dir(s.ovl.MergedDir(id))
}

// OverlayIDs lists the container ids that have an overlay directory (not
// the shared layer store).
func (s *Supervisor) OverlayIDs() []string { return s.ovl.ContainerIDs() }

// LayerStoreDir is the shared store of extracted image layers.
func (s *Supervisor) LayerStoreDir() string { return s.ovl.LayerStoreDir() }

// OverlayMounted reports whether a container's merged directory is mounted.
func (s *Supervisor) OverlayMounted(id string) bool { return s.ovl.IsMounted(id) }

// RemoveOrphanOverlay deletes the overlay of a container that is not
// supervised and not mounted (left behind by a crash).
func (s *Supervisor) RemoveOrphanOverlay(id string) error {
	if s.ovl.IsReserved(id) || id == "" || strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("%q is not a container overlay", id)
	}
	if s.Running(id) {
		return fmt.Errorf("container %s is running", id)
	}
	if s.ovl.IsMounted(id) {
		return fmt.Errorf("overlay of %s is still mounted", id)
	}
	return s.ovl.Remove(id)
}
