//go:build linux

package control_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// ReplaceImage swaps a running container's spec, and puts the previous one
// back when the new container does not pass its health check.
func TestControlReplaceImageRollback(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "upd", "args": []string{"sleep", "300"}, "restart": "no",
		"labels": map[string]string{supervisor.UpdateLabel: "digest"}})
	cands := d.Sup.UpdateCandidates()
	if len(cands) != 1 || cands[0].ID != "upd" || cands[0].Labels[supervisor.UpdateLabel] != "digest" {
		t.Fatalf("candidates: %+v", cands)
	}
	old := cands[0]
	ctx := context.Background()

	// A good new spec replaces the old one.
	good := old
	good.Env = append(append([]string{}, old.Env...), "VERSION=2")
	if err := d.Sup.ReplaceImage(ctx, good); err != nil {
		t.Fatal(err)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/upd/exec", map[string]any{"argv": []string{"env"}}); code != 200 || !strings.Contains(out, "VERSION=2") {
		t.Fatalf("after update: %d %s", code, out)
	}

	// A new spec whose health check fails: rolled back to the running one.
	bad := good
	bad.Env = append(append([]string{}, good.Env...), "VERSION=3")
	bad.Healthcheck = &health.Config{Type: health.TypeExec, Command: []string{"false"}, Interval: 100 * time.Millisecond,
		Timeout: time.Second, Retries: 1}
	err := d.Sup.ReplaceImage(ctx, bad)
	if !errors.Is(err, supervisor.ErrRolledBack) {
		t.Fatalf("want a rollback, got %v", err)
	}
	if !d.Sup.Running("upd") {
		t.Fatal("not running after rollback")
	}
	code, out := d.Do(t, "POST", "/v1/containers/upd/exec", map[string]any{"argv": []string{"env"}})
	if code != 200 || !strings.Contains(out, "VERSION=2") || strings.Contains(out, "VERSION=3") {
		t.Fatalf("after rollback: %d %s", code, out)
	}
	if code, out := d.Do(t, "GET", "/v1/updates", nil); code != 200 || !strings.Contains(out, `"id":"upd"`) {
		t.Errorf("GET /v1/updates: %d %s", code, out)
	}
	if err := d.Sup.ReplaceImage(ctx, supervisor.ContainerSpec{ID: "nope"}); err == nil {
		t.Error("replace of a missing container")
	}
	d.Do(t, "POST", "/v1/containers/upd/stop", nil)
}

func TestControlInspectLabels(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "lab", "args": []string{"sleep", "300"}, "restart": "no", "labels": map[string]string{"team": "web"}})
	if code, out := d.Do(t, "GET", "/v1/containers/lab/inspect", nil); code != 200 || !strings.Contains(out, `"labels":{"team":"web"}`) {
		t.Errorf("inspect: %d %s", code, out)
	}
	d.Do(t, "POST", "/v1/containers/lab/stop", nil)
}
