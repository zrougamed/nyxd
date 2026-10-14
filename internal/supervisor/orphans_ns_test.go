//go:build linux

package supervisor_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// Running containers with no supervisor record are adopted from their bundle
// metadata only when it is usable.
func TestOrphanReadoptSkips(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	ids := []string{"nometa", "noimage", "badpub"}
	for _, id := range ids {
		if err := d.Sup.Start(ctx, spec(t, d, id, supervisor.RestartNever, "", "sleep", "300")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(d.Base, "supervisor", "containers", id+".json")); err != nil {
			t.Fatal(err)
		}
	}
	editMeta := func(id string, change func(map[string]any)) {
		p := filepath.Join(d.Base, "bundles", id, "nyxd-meta.json")
		var m map[string]any
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(b, &m) //nolint:errcheck
		change(m)
		b, _ = json.Marshal(m)
		os.WriteFile(p, b, 0o600) //nolint:errcheck
	}
	os.Remove(filepath.Join(d.Base, "bundles", "nometa", "nyxd-meta.json")) //nolint:errcheck
	editMeta("noimage", func(m map[string]any) { m["image"] = "absent/app:1" })
	editMeta("badpub", func(m map[string]any) { m["publish"] = []string{"not-a-port"} })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup2 := supervisor.New(d.Runtime, d.Overlay, network.NewHostBackend(), d.Base, log, d.Logs, d.Store, nil, "host", "auto", "")
	sup2.SetRootless(true, false)
	if got := strings.Join(sup2.List(), ","); got != "badpub" {
		t.Errorf("adopted %q, want only badpub", got)
	}
	if err := sup2.Kill(ctx, "badpub", "KILL"); err != nil {
		t.Fatal(err)
	}
	daemon.WaitFor(t, 30*time.Second, "badpub to end", func() bool { return len(sup2.List()) == 0 })
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sup2.Shutdown(sctx)
}

// An image stop signal crun does not know: the stop falls back to KILL.
func TestStopWithBadStopSignal(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := setup(t)
	ctx := context.Background()
	sp := spec(t, d, "badsig", supervisor.RestartNever, "", "sleep", "300")
	cfg := *sp.ImageConfig
	cfg.Config.StopSignal = "SIGBOGUS"
	sp.ImageConfig = &cfg
	if err := d.Sup.Start(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.Stop(ctx, "badsig"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !daemonLogHas(d, "graceful stop failed, forcing") {
		t.Error("no forced stop")
	}
	if err := d.Sup.KillForRestart(ctx, "absent"); err == nil {
		t.Error("KillForRestart of an unknown container")
	}
}
