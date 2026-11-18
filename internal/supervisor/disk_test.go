package supervisor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/overlay"
)

func TestVolumeInUseAndOrphanOverlayGuards(t *testing.T) {
	base := t.TempDir()
	ovl, err := overlay.NewManager(filepath.Join(base, "overlay"), overlay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{baseDir: base, ovl: ovl, containers: map[string]*containerEntry{}, log: quiet}
	vol := filepath.Join(base, "volumes", "p", "data")
	s.containers["live"] = &containerEntry{spec: ContainerSpec{ID: "live", ExtraMounts: []bundle.Mount{{Source: filepath.Join(base, "volumes", "p", "logs"), Destination: "/logs"}}}}
	if s.VolumeInUse(vol) {
		t.Fatal("unused volume reported in use")
	}
	// An exited container keeps its volume: nyx start needs the data.
	s.recordExited(ContainerSpec{ID: "old", ExtraMounts: []bundle.Mount{{Source: filepath.Join(vol, "sub"), Destination: "/data"}}}, 0)
	if !s.VolumeInUse(vol) || !s.VolumeInUse(filepath.Join(base, "volumes", "p", "logs")) {
		t.Fatal("volume of an exited or running container not in use")
	}
	for _, id := range []string{"_layers", "_cache", "", "../x", "live"} {
		if err := s.RemoveOrphanOverlay(id); err == nil {
			t.Errorf("RemoveOrphanOverlay(%q) allowed", id)
		} else if id == "_layers" && !strings.Contains(err.Error(), "not a container overlay") {
			t.Errorf("_layers: %v", err)
		}
	}
	if err := s.RemoveOrphanOverlay("ghost"); err != nil {
		t.Errorf("ghost: %v", err)
	}
	if s.LayerStoreDir() != filepath.Join(base, "overlay", "_layers") || len(s.OverlayIDs()) != 0 || s.OverlayMounted("ghost") {
		t.Error("overlay helpers")
	}
}

func TestCheckContainerID(t *testing.T) {
	for _, id := range []string{"_layers", "_cache", "_x"} {
		if err := CheckContainerID(id); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"web", "shop-db", "a_b", "x_"} {
		if err := CheckContainerID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	// Start refuses before touching anything.
	s := &Supervisor{containers: map[string]*containerEntry{}, log: quiet}
	if err := s.Start(context.Background(), ContainerSpec{ID: "_layers"}); err == nil {
		t.Error("Start accepted _layers")
	}
	if err := s.RunAttached(context.Background(), ContainerSpec{ID: "_layers"}, nil, nil, nil); err == nil {
		t.Error("RunAttached accepted _layers")
	}
	if len(s.containers) != 0 {
		t.Error("entry registered for a refused id")
	}
}
