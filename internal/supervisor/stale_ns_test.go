//go:build linux

package supervisor

import (
	"context"
	"os/exec"
	"testing"

	"github.com/zrougamed/nyxd/internal/runtime"
	"github.com/zrougamed/nyxd/internal/testutil/appbin"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// A container crun still runs but no supervisor knows (left by a daemon that
// died) is deleted before a fresh start under the same id.
func TestStaleCrunStateIsCleared(t *testing.T) {
	if _, err := exec.LookPath("crun"); err != nil {
		t.Skip("crun not installed")
	}
	if !nsrun.Run(t) {
		return
	}
	dir := t.TempDir()
	rt, err := runtime.New("crun", dir+"/crun")
	if err != nil {
		t.Fatal(err)
	}
	rt.SetCgroupManager("disabled")
	ctx := context.Background()
	// crun run --detach with no captured stdio, as a vanished daemon leaves it.
	b := appbin.Bundle(t, dir, "stale", "sleep", "300")
	args := append(rt.GlobalArgs(), "--root", rt.RootDir(), "run", "--detach", "--bundle", b, "stale")
	// No captured stdio: the container would inherit the pipe and keep it open.
	if err := exec.Command(rt.Binary(), args...).Run(); err != nil {
		t.Fatalf("crun run --detach: %v", err)
	}
	s := &Supervisor{rt: rt, log: quiet}
	s.ensureCrunIDFreeForFreshStart(ctx, "stale", quiet)
	if _, err := rt.State(ctx, "stale"); !runtime.CrunContainerAbsent(err) {
		t.Fatalf("stale state kept: %v", err)
	}
	s.ensureCrunIDFreeForFreshStart(ctx, "stale", quiet) // nothing to do
}
