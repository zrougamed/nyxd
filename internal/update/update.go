// Package update keeps opted-in containers on their tag's current image:
// containers labelled nyx.update=digest are recreated when the registry's
// digest for their tag changes, and put back on the previous image when the
// new one does not start or does not pass its health check.
package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// Containers is what the updater needs from the supervisor.
type Containers interface {
	UpdateCandidates() []supervisor.ContainerSpec
	ReplaceImage(ctx context.Context, want supervisor.ContainerSpec) error
}

// Images is what the updater needs from the image store.
type Images interface {
	SourceDigest(ref string) string
	RemoteDigest(ctx context.Context, ref string, auth *image.RegistryAuth) (string, error)
	PullWithProgress(ctx context.Context, ref string, auth *image.RegistryAuth, on func(image.PullEvent)) (*oci.ImageConfig, error)
	ResolvePulledImage(ref string) (*oci.Manifest, *oci.ImageConfig, []string, error)
	Tag(src, dst string) error
}

// Outcomes of a check or an update (Result.Action).
const (
	UpToDate   = "up to date"
	Available  = "update available"
	Unknown    = "unknown (no recorded digest: the next update pulls once to compare)"
	Updated    = "updated"
	RolledBack = "rolled back"
	Skipped    = "skipped (this digest failed before)"
	Failed     = "error"
)

// Result is what happened, or would happen, to one container.
type Result struct {
	ID        string `json:"id"`
	Image     string `json:"image"`
	Current   string `json:"current,omitempty"`
	Available string `json:"available,omitempty"`
	Action    string `json:"action"`
	Error     string `json:"error,omitempty"`
}

// Updater checks and updates opted-in containers.
type Updater struct {
	containers Containers
	images     Images
	events     *events.Bus
	log        *slog.Logger

	mu     sync.Mutex
	failed map[string]string // container id -> digest whose update was rolled back
	runMu  sync.Mutex        // one round at a time
}

// New returns an updater.
func New(c Containers, imgs Images, bus *events.Bus, log *slog.Logger) *Updater {
	return &Updater{containers: c, images: imgs, events: bus, log: log, failed: map[string]string{}}
}

// RollbackRef is the local tag that keeps a container's previous image
// (and its layers) until its next successful update.
func RollbackRef(containerID string) string { return "nyx-rollback/" + containerID + ":previous" }

// Check reports what an update would do, without pulling or recreating.
func (u *Updater) Check(ctx context.Context) []Result {
	var out []Result
	for _, spec := range u.containers.UpdateCandidates() {
		r := Result{ID: spec.ID, Image: spec.Image, Current: u.images.SourceDigest(spec.Image)}
		remote, err := u.images.RemoteDigest(ctx, spec.Image, nil)
		switch {
		case err != nil:
			r.Action, r.Error = Failed, err.Error()
		case u.failedDigest(spec.ID) == remote:
			r.Available, r.Action = remote, Skipped
		case r.Current == "":
			r.Available, r.Action = remote, Unknown
		case r.Current == remote:
			r.Action = UpToDate
		default:
			r.Available, r.Action = remote, Available
		}
		out = append(out, r)
	}
	return out
}

// Run updates every opted-in container whose tag moved, one at a time.
func (u *Updater) Run(ctx context.Context) []Result {
	u.runMu.Lock()
	defer u.runMu.Unlock()
	var out []Result
	for _, spec := range u.containers.UpdateCandidates() {
		out = append(out, u.updateOne(ctx, spec))
	}
	return out
}

func (u *Updater) failedDigest(id string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.failed[id]
}

func (u *Updater) updateOne(ctx context.Context, spec supervisor.ContainerSpec) Result {
	r := Result{ID: spec.ID, Image: spec.Image, Current: u.images.SourceDigest(spec.Image)}
	fail := func(err error) Result {
		r.Action, r.Error = Failed, err.Error()
		u.log.Warn("image update", "id", spec.ID, "image", spec.Image, "err", err)
		return r
	}
	remote, err := u.images.RemoteDigest(ctx, spec.Image, nil)
	if err != nil {
		return fail(err)
	}
	r.Available = remote
	if u.failedDigest(spec.ID) == remote {
		r.Action = Skipped
		return r
	}
	if r.Current != "" && r.Current == remote {
		r.Action, r.Available = UpToDate, ""
		return r
	}
	// Keep the running image (and its layers) under a local tag first, so
	// a rollback never needs the network.
	if err := u.images.Tag(spec.Image, RollbackRef(spec.ID)); err != nil {
		u.log.Warn("image update: keep previous image", "id", spec.ID, "err", err)
	}
	if _, err := u.images.PullWithProgress(ctx, spec.Image, nil, nil); err != nil {
		return fail(fmt.Errorf("pull: %w", err))
	}
	m, cfg, paths, err := u.images.ResolvePulledImage(spec.Image)
	if err != nil {
		return fail(err)
	}
	if sameImage(spec, m, cfg) { // the digest moved, the image did not (or nyxd had no record)
		r.Action, r.Available = UpToDate, ""
		return r
	}
	want := spec
	want.ImageConfig, want.ManifestLayers, want.BlobPaths = cfg, m.Layers, paths
	from := r.Current
	if err := u.containers.ReplaceImage(ctx, want); err != nil {
		if !errors.Is(err, supervisor.ErrRolledBack) {
			return fail(err)
		}
		// Point the tag back at what runs, and do not retry this digest.
		if terr := u.images.Tag(RollbackRef(spec.ID), spec.Image); terr != nil {
			u.log.Warn("image update: restore tag", "id", spec.ID, "err", terr)
		}
		u.mu.Lock()
		u.failed[spec.ID] = remote
		u.mu.Unlock()
		r.Action, r.Error = RolledBack, err.Error()
		u.events.Publish(events.Event{Type: events.TypeContainer, Action: events.UpdateRolledBack, ID: spec.ID, Image: spec.Image,
			Attrs: map[string]string{"digest": remote, "error": err.Error()}})
		u.log.Warn("image update rolled back", "id", spec.ID, "image", spec.Image, "digest", remote, "err", err)
		return r
	}
	u.mu.Lock()
	delete(u.failed, spec.ID)
	u.mu.Unlock()
	r.Action = Updated
	u.events.Publish(events.Event{Type: events.TypeContainer, Action: events.Update, ID: spec.ID, Image: spec.Image,
		Attrs: map[string]string{"from": from, "to": remote}})
	u.log.Info("image updated", "id", spec.ID, "image", spec.Image, "digest", remote)
	return r
}

// sameImage reports whether the pulled image is the one spec runs.
func sameImage(spec supervisor.ContainerSpec, m *oci.Manifest, cfg *oci.ImageConfig) bool {
	if !slices.EqualFunc(spec.ManifestLayers, m.Layers, func(a, b oci.Descriptor) bool { return a.Digest == b.Digest }) {
		return false
	}
	if spec.ImageConfig == nil || cfg == nil {
		return spec.ImageConfig == cfg
	}
	a, _, errA := image.ConfigDigest(spec.ImageConfig)
	b, _, errB := image.ConfigDigest(cfg)
	return errA == nil && errB == nil && a == b
}

// Loop runs an update round every interval, plus up to 10% jitter so many
// hosts do not hit the registry at the same second, until ctx ends.
func (u *Updater) Loop(ctx context.Context, interval time.Duration) {
	for {
		wait := interval + time.Duration(rand.Int64N(int64(interval)/10+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		for _, r := range u.Run(ctx) {
			if r.Action == Updated || r.Action == RolledBack || r.Action == Failed {
				u.log.Info("update round", "id", r.ID, "image", r.Image, "action", r.Action, "error", r.Error)
			}
		}
	}
}
