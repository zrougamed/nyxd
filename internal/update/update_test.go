package update

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/pkg/oci"
)

type fakeContainers struct {
	specs    []supervisor.ContainerSpec
	replaced []supervisor.ContainerSpec
	err      error
}

func (f *fakeContainers) UpdateCandidates() []supervisor.ContainerSpec { return f.specs }
func (f *fakeContainers) ReplaceImage(_ context.Context, want supervisor.ContainerSpec) error {
	f.replaced = append(f.replaced, want)
	return f.err
}

type fakeImages struct {
	source, remote map[string]string
	remoteErr      error
	pullErr        error
	pulled         *oci.Manifest
	tags           [][2]string
	pulls          int
}

func (f *fakeImages) SourceDigest(ref string) string { return f.source[ref] }
func (f *fakeImages) RemoteDigest(context.Context, string, *image.RegistryAuth) (string, error) {
	if f.remoteErr != nil {
		return "", f.remoteErr
	}
	return f.remote["app:1"], nil
}
func (f *fakeImages) PullWithProgress(context.Context, string, *image.RegistryAuth, func(image.PullEvent)) (*oci.ImageConfig, error) {
	f.pulls++
	return nil, f.pullErr
}
func (f *fakeImages) ResolvePulledImage(string) (*oci.Manifest, *oci.ImageConfig, []string, error) {
	return f.pulled, &oci.ImageConfig{OS: "linux"}, []string{"/blobs/new"}, nil
}
func (f *fakeImages) Tag(src, dst string) error {
	f.tags = append(f.tags, [2]string{src, dst})
	return nil
}

func newFixture() (*Updater, *fakeContainers, *fakeImages, *events.Bus) {
	spec := supervisor.ContainerSpec{ID: "web", Image: "app:1", ImageConfig: &oci.ImageConfig{OS: "linux"},
		ManifestLayers: []oci.Descriptor{{Digest: "sha256:old"}}}
	c := &fakeContainers{specs: []supervisor.ContainerSpec{spec}}
	imgs := &fakeImages{source: map[string]string{"app:1": "sha256:aaa"}, remote: map[string]string{"app:1": "sha256:bbb"},
		pulled: &oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:new"}}}}
	bus := events.New(10)
	return New(c, imgs, bus, slog.New(slog.NewTextHandler(io.Discard, nil))), c, imgs, bus
}

func TestUpdateAndRollback(t *testing.T) {
	u, c, imgs, bus := newFixture()
	if res := u.Check(context.Background()); len(res) != 1 || res[0].Action != Available || res[0].Available != "sha256:bbb" || imgs.pulls != 0 {
		t.Fatalf("check: %+v", res)
	}
	// Update: previous image kept under the rollback tag, then pulled and replaced.
	res := u.Run(context.Background())
	if res[0].Action != Updated || len(c.replaced) != 1 || c.replaced[0].BlobPaths[0] != "/blobs/new" || c.replaced[0].ManifestLayers[0].Digest != "sha256:new" {
		t.Fatalf("update: %+v %+v", res, c.replaced)
	}
	if len(imgs.tags) != 1 || imgs.tags[0] != [2]string{"app:1", RollbackRef("web")} {
		t.Fatalf("rollback tag: %v", imgs.tags)
	}

	// Rollback: the tag goes back to what runs, the digest is not retried.
	u2, c2, imgs2, bus2 := newFixture()
	c2.err = errors.Join(supervisor.ErrRolledBack, errors.New("readiness: unhealthy"))
	if res := u2.Run(context.Background()); res[0].Action != RolledBack || res[0].Error == "" {
		t.Fatalf("rollback: %+v", res)
	}
	if last := imgs2.tags[len(imgs2.tags)-1]; last != [2]string{RollbackRef("web"), "app:1"} {
		t.Fatalf("tag not restored: %v", imgs2.tags)
	}
	pulls := imgs2.pulls
	if res := u2.Run(context.Background()); res[0].Action != Skipped || imgs2.pulls != pulls {
		t.Fatalf("retried a failed digest: %+v", res)
	}
	if res := u2.Check(context.Background()); res[0].Action != Skipped {
		t.Fatalf("check after rollback: %+v", res)
	}
	// A newer digest is tried again.
	imgs2.remote["app:1"] = "sha256:ccc"
	c2.err = nil
	if res := u2.Run(context.Background()); res[0].Action != Updated {
		t.Fatalf("newer digest: %+v", res)
	}

	for _, b := range []*events.Bus{bus, bus2} {
		past, sub := b.Subscribe(time.Unix(0, 0), nil)
		sub.Close()
		if len(past) == 0 {
			t.Fatal("no events")
		}
	}
	past, sub := bus2.Subscribe(time.Unix(0, 0), events.Filter{"event": {events.UpdateRolledBack}})
	sub.Close()
	if len(past) != 1 || past[0].Attrs["digest"] != "sha256:bbb" {
		t.Fatalf("rollback event: %+v", past)
	}
}

func TestUpdateUpToDateAndErrors(t *testing.T) {
	u, c, imgs, _ := newFixture()
	imgs.remote["app:1"] = "sha256:aaa"
	if res := u.Run(context.Background()); res[0].Action != UpToDate || imgs.pulls != 0 || len(c.replaced) != 0 {
		t.Fatalf("up to date: %+v", res)
	}
	if res := u.Check(context.Background()); res[0].Action != UpToDate {
		t.Fatalf("check up to date: %+v", res)
	}
	// No recorded digest and the pulled image is the running one: no restart.
	u2, c2, imgs2, _ := newFixture()
	imgs2.source = map[string]string{}
	imgs2.pulled = &oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:old"}}}
	if res := u2.Check(context.Background()); res[0].Action != Unknown {
		t.Fatalf("check unknown: %+v", res)
	}
	if res := u2.Run(context.Background()); res[0].Action != UpToDate || len(c2.replaced) != 0 || imgs2.pulls != 1 {
		t.Fatalf("unknown digest, same image: %+v", res)
	}
	u3, _, imgs3, _ := newFixture()
	imgs3.remoteErr = errors.New("registry down")
	if res := u3.Run(context.Background()); res[0].Action != Failed {
		t.Fatalf("remote error: %+v", res)
	}
	if res := u3.Check(context.Background()); res[0].Action != Failed {
		t.Fatalf("check remote error: %+v", res)
	}
	u4, _, imgs4, _ := newFixture()
	imgs4.pullErr = errors.New("pull failed")
	if res := u4.Run(context.Background()); res[0].Action != Failed {
		t.Fatalf("pull error: %+v", res)
	}
	u5, c5, _, _ := newFixture()
	c5.err = errors.New("stop failed")
	if res := u5.Run(context.Background()); res[0].Action != Failed {
		t.Fatalf("replace error: %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u.Loop(ctx, time.Hour) // returns at once on a done context
	if !sameImage(supervisor.ContainerSpec{}, &oci.Manifest{}, nil) {
		t.Error("nil configs")
	}
}
