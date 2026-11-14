//go:build linux

package control_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// listAll returns the status (with exit code) of every container, exited ones included.
func listAll(t *testing.T, d *daemon.Daemon) map[string]string {
	t.Helper()
	_, out := d.Do(t, "GET", "/v1/containers?detail=1&all=1", nil)
	var resp struct {
		Items []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			ExitCode *int   `json:"exit_code"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("list: %v: %s", err, out)
	}
	m := map[string]string{}
	for _, it := range resp.Items {
		st := it.Status
		if it.ExitCode != nil {
			st += " " + strconv.Itoa(*it.ExitCode)
		}
		m[it.ID] = st
	}
	return m
}

func countLog(t *testing.T, d *daemon.Daemon, id, line string) int {
	return strings.Count(logsOf(t, d, id, "?tail=1000"), line)
}

func waitCount(t *testing.T, d *daemon.Daemon, id, line string, n int) {
	t.Helper()
	daemon.WaitFor(t, 15*time.Second, id+" log "+line, func() bool { return countLog(t, d, id, line) >= n })
}

func TestControlStartRestart(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)

	run(t, d, map[string]any{"id": "life", "args": []string{"trapterm"}, "restart": "no", "env": []string{"KEEP=1"}})
	waitLog(t, d, "life", "ready")

	// Restart a running container: SIGTERM, then the same spec again.
	if code, out := d.Do(t, "POST", "/v1/containers/life/restart?t=5", nil); code != 200 {
		t.Fatalf("restart: %d %s", code, out)
	}
	waitCount(t, d, "life", "ready", 2)
	if countLog(t, d, "life", "got TERM") != 1 {
		t.Errorf("restart did not stop gracefully: %s", logsOf(t, d, "life", ""))
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/life/restart?t=x", nil); code != 400 {
		t.Errorf("bad t: %d", code)
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/life/start", nil); code != 409 {
		t.Errorf("start while running: %d", code)
	}

	// Stopped: listed with -a only, with its exit code.
	if code, out := d.Do(t, "POST", "/v1/containers/life/stop", nil); code != 200 {
		t.Fatalf("stop: %d %s", code, out)
	}
	if st := listAll(t, d)["life"]; st != "exited 0" {
		t.Errorf("after stop: status %q", st)
	}
	if _, out := d.Do(t, "GET", "/v1/containers?detail=1", nil); strings.Contains(out, `"life"`) {
		t.Errorf("exited container in the default list: %s", out)
	}
	// Its image survives a prune.
	d.Do(t, "POST", "/v1/images/prune", map[string]any{})
	if _, out := d.Do(t, "GET", "/v1/images", nil); !strings.Contains(out, "test/app") {
		t.Errorf("image of an exited container pruned: %s", out)
	}

	// Start it again, by prefix, with the same settings.
	if code, out := d.Do(t, "POST", "/v1/containers/lif/start", nil); code != 200 {
		t.Fatalf("start: %d %s", code, out)
	}
	waitCount(t, d, "life", "ready", 3)
	if st := listAll(t, d)["life"]; st == "exited 0" {
		t.Errorf("still listed as exited after start")
	}
	_, out := d.Do(t, "GET", "/v1/containers/life/inspect", nil)
	if !strings.Contains(out, "KEEP=1") {
		t.Errorf("env lost on start: %s", out)
	}

	// Restart of an exited container just starts it.
	d.Do(t, "POST", "/v1/containers/life/stop", nil)
	if code, out := d.Do(t, "POST", "/v1/containers/life/restart", nil); code != 200 {
		t.Fatalf("restart exited: %d %s", code, out)
	}
	waitCount(t, d, "life", "ready", 4)

	// Remove: gone from every list, nothing left to start.
	if code, out := d.Do(t, "POST", "/v1/containers/life/remove", nil); code != 200 {
		t.Fatalf("remove: %d %s", code, out)
	}
	if _, ok := listAll(t, d)["life"]; ok {
		t.Error("removed container still listed")
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/life/start", nil); code != 404 {
		t.Errorf("start removed: %d", code)
	}
	if code, _ := d.Do(t, "POST", "/v1/containers/life/restart", nil); code != 404 {
		t.Errorf("restart removed: %d", code)
	}

	// Removing an exited container drops its record and its logs.
	run(t, d, map[string]any{"id": "gone", "args": []string{"trapterm"}, "restart": "no"})
	waitLog(t, d, "gone", "ready")
	d.Do(t, "POST", "/v1/containers/gone/stop", nil)
	if code, out := d.Do(t, "POST", "/v1/containers/gone/remove", nil); code != 200 {
		t.Fatalf("remove exited: %d %s", code, out)
	}
	if _, ok := listAll(t, d)["gone"]; ok {
		t.Error("removed exited container still listed")
	}
	if code, _ := d.Do(t, "GET", "/v1/containers/gone/logs", nil); code != 404 {
		t.Errorf("logs of removed exited container: %d", code)
	}

	// --rm containers can be restarted while running, and leave no record.
	run(t, d, map[string]any{"id": "tmp", "args": []string{"trapterm"}, "restart": "no", "auto_remove": true})
	waitLog(t, d, "tmp", "ready")
	if code, out := d.Do(t, "POST", "/v1/containers/tmp/restart", nil); code != 200 {
		t.Fatalf("restart --rm: %d %s", code, out)
	}
	daemon.WaitFor(t, 15*time.Second, "tmp running again", func() bool { return listAll(t, d)["tmp"] == "running" })
	d.Do(t, "POST", "/v1/containers/tmp/stop", nil)
	if _, ok := listAll(t, d)["tmp"]; ok {
		t.Error("--rm container left a record")
	}

	// A new run under the name of an exited container replaces it.
	run(t, d, map[string]any{"id": "again", "args": []string{"trapterm"}, "restart": "no"})
	waitLog(t, d, "again", "ready")
	d.Do(t, "POST", "/v1/containers/again/stop", nil)
	run(t, d, map[string]any{"id": "again", "args": []string{"trapterm"}, "restart": "no"})
	daemon.WaitFor(t, 15*time.Second, "again running", func() bool { return listAll(t, d)["again"] == "running" })
	d.Do(t, "POST", "/v1/containers/again/remove", nil)
}

func TestControlComposeStartRestart(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	yaml := `services:
  db:
    image: test/app:1
    command: [trapterm]
  api:
    image: test/app:1
    command: [trapterm]
    depends_on: [db]
`
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"yaml": yaml, "context_dir": t.TempDir(), "project": "st"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	if code, out := d.Do(t, "POST", "/v1/compose/start", body(nil)); code != 404 || !strings.Contains(out, "nyx compose up") {
		t.Errorf("start before up: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/up", body(nil)); code != 200 {
		t.Fatalf("up: %d %s", code, out)
	}
	waitLog(t, d, "st-api", "ready")

	// start leaves running services alone.
	if code, out := d.Do(t, "POST", "/v1/compose/start", body(nil)); code != 200 || strings.Contains(out, "st-") {
		t.Errorf("start of a running stack: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/stop", body(nil)); code != 200 {
		t.Fatalf("stop: %d %s", code, out)
	}
	code, out := d.Do(t, "POST", "/v1/compose/start", body(nil))
	if code != 200 || !strings.Contains(out, `"started":["st-db","st-api"]`) {
		t.Fatalf("start: %d %s", code, out)
	}
	waitCount(t, d, "st-api", "ready", 2)

	code, out = d.Do(t, "POST", "/v1/compose/restart", body(map[string]any{"services": []string{"api"}}))
	if code != 200 || !strings.Contains(out, `"restarted":["st-api"]`) {
		t.Fatalf("restart api: %d %s", code, out)
	}
	waitCount(t, d, "st-api", "ready", 3)
	if n := countLog(t, d, "st-db", "ready"); n != 2 {
		t.Errorf("db restarted with api only: ready x%d", n)
	}
	if code, _ := d.Do(t, "POST", "/v1/compose/restart", body(map[string]any{"services": []string{"nope"}})); code != 400 {
		t.Errorf("unknown service: %d", code)
	}
	if code, _ := d.Do(t, "POST", "/v1/compose/start", map[string]any{}); code != 400 {
		t.Errorf("no document: %d", code)
	}

	// down after stop removes the exited records too.
	d.Do(t, "POST", "/v1/compose/stop", body(nil))
	if code, out := d.Do(t, "POST", "/v1/compose/down", body(nil)); code != 200 {
		t.Fatalf("down: %d %s", code, out)
	}
	all := listAll(t, d)
	if _, ok := all["st-db"]; ok {
		t.Errorf("down left records: %v", all)
	}
}

// editRecord rewrites the saved spec of an exited container.
func editRecord(t *testing.T, d *daemon.Daemon, id string, edit func(spec map[string]any)) {
	t.Helper()
	p := filepath.Join(d.Base, "supervisor", "exited", id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	edit(rec["spec"].(map[string]any))
	if b, err = json.Marshal(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestControlStartImageChanges(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	run(t, d, map[string]any{"id": "img", "args": []string{"trapterm"}, "restart": "no"})
	waitLog(t, d, "img", "ready")
	d.Do(t, "POST", "/v1/containers/img/stop", nil)

	// Layers gone (pruned after a newer pull of the tag): the current image is used.
	editRecord(t, d, "img", func(spec map[string]any) { spec["BlobPaths"] = []string{"/nonexistent/blob"} })
	if code, out := d.Do(t, "POST", "/v1/containers/img/start", nil); code != 200 {
		t.Fatalf("start with pruned layers: %d %s", code, out)
	}
	waitCount(t, d, "img", "ready", 2)
	d.Do(t, "POST", "/v1/containers/img/stop", nil)

	// The image itself is gone: a clear error, the record stays for a later try.
	editRecord(t, d, "img", func(spec map[string]any) {
		spec["BlobPaths"] = []string{"/nonexistent/blob"}
		spec["Image"] = "gone/app:1"
	})
	code, out := d.Do(t, "POST", "/v1/containers/img/start", nil)
	if code != 400 || !strings.Contains(out, "no longer in the store") {
		t.Errorf("start without image: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/img/restart", nil); code != 400 {
		t.Errorf("restart without image: %d %s", code, out)
	}
	if _, ok := listAll(t, d)["img"]; !ok {
		t.Error("record dropped after a failed start")
	}
	code, out = d.Do(t, "POST", "/v1/compose/start", map[string]any{
		"yaml": "services:\n  img:\n    image: test/app:1\n", "context_dir": t.TempDir(), "project": "x"})
	if code != 404 {
		t.Errorf("compose start of a project without containers: %d %s", code, out)
	}
}
