//go:build linux

package control_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlSystemDFPrune(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "live", "args": []string{"sleep", "300"}, "restart": "no"})
	run(t, d, map[string]any{"id": "done", "args": []string{"sh", "-c", "echo bye"}, "restart": "no"})
	daemon.WaitFor(t, 15*time.Second, "done exited", func() bool {
		_, out := d.Do(t, "GET", "/v1/containers?detail=1&all=1", nil)
		return strings.Contains(out, `"done"`) && strings.Contains(out, "exited")
	})
	// Leftovers: an overlay and a log of containers that no longer exist, a
	// stale build directory, and an unused volume.
	mk := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(d.Base, "overlay", "ghost", "diff", "f"), "leftover")
	mk(filepath.Join(d.Base, "logs", "ghost.log"), "old log")
	stale := filepath.Join(d.Base, "build", "contexts", "ctx-old")
	mk(filepath.Join(stale, "Dockerfile"), "FROM scratch")
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(stale, old, old)
	vol := filepath.Join(d.Base, "volumes", "proj", "data")
	mk(filepath.Join(vol, "db"), "rows")

	type category struct {
		Type        string `json:"type"`
		Count       int    `json:"count"`
		Active      int    `json:"active"`
		Size        int64  `json:"size"`
		Reclaimable int64  `json:"reclaimable"`
	}
	df := func() map[string]category {
		code, out := d.Do(t, "GET", "/v1/system/df", nil)
		if code != 200 {
			t.Fatalf("df: %d %s", code, out)
		}
		var r struct {
			Categories []category `json:"categories"`
		}
		_ = json.Unmarshal([]byte(out), &r)
		m := map[string]category{}
		for _, c := range r.Categories {
			m[c.Type] = c
		}
		return m
	}
	before := df()
	if c := before["containers"]; c.Count != 2 || c.Active != 1 || c.Reclaimable == 0 {
		t.Errorf("containers: %+v", c)
	}
	if c := before["volumes"]; c.Count != 1 || c.Active != 0 || c.Reclaimable == 0 {
		t.Errorf("volumes: %+v", c)
	}
	if c := before["images"]; c.Count != 1 || c.Active != 1 || c.Size == 0 {
		t.Errorf("images: %+v", c)
	}

	prune := func(body map[string]any) map[string][]string {
		code, out := d.Do(t, "POST", "/v1/system/prune", body)
		if code != 200 {
			t.Fatalf("prune %v: %d %s", body, code, out)
		}
		var r struct {
			Removed map[string][]string `json:"removed"`
		}
		_ = json.Unmarshal([]byte(out), &r)
		return r.Removed
	}
	// A dry run lists and removes nothing.
	if got := prune(map[string]any{"dry_run": true}); strings.Join(got["containers"], ",") != "done" || strings.Join(got["overlays"], ",") != "ghost" {
		t.Errorf("dry run: %v", got)
	}
	if _, err := os.Stat(filepath.Join(d.Base, "overlay", "ghost")); err != nil {
		t.Fatal("dry run removed the overlay")
	}
	// The default prune: exited container, ghost log and overlay, stale
	// build dir; the volume and everything "live" uses stay.
	got := prune(map[string]any{})
	if strings.Join(got["containers"], ",") != "done" || strings.Join(got["logs"], ",") != "ghost" || strings.Join(got["overlays"], ",") != "ghost" {
		t.Errorf("prune: %v", got)
	}
	for _, p := range []string{filepath.Join(d.Base, "overlay", "ghost"), stale} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	for _, p := range []string{vol, filepath.Join(d.Base, "overlay", "live"), filepath.Join(d.Base, "overlay", "_layers")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	if code, out := d.Do(t, "POST", "/v1/containers/live/exec", map[string]any{"argv": []string{"cat", "/bin/sh"}}); code != 200 {
		t.Errorf("live container broken after prune: %d %s", code, out)
	}
	// --volumes removes the unused volume; --images keeps the image "live" uses.
	got = prune(map[string]any{"volumes": true, "images": true})
	if strings.Join(got["volumes"], ",") != "proj/data" || len(got["images"]) != 0 {
		t.Errorf("volumes/images: %v", got)
	}
	if _, err := os.Stat(vol); err == nil {
		t.Error("unused volume kept with --volumes")
	}
	d.Do(t, "POST", "/v1/containers/live/stop", nil)
}
