//go:build linux

package control_test

import (
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlComposeDependsOnConditions(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	up := func(project, yaml string, extra map[string]any) (int, string) {
		body := map[string]any{"yaml": yaml, "context_dir": dir, "project": project}
		for k, v := range extra {
			body[k] = v
		}
		return d.Do(t, "POST", "/v1/compose/up", body)
	}
	running := func(id string) bool {
		_, out := d.Do(t, "GET", "/v1/containers", nil)
		return strings.Contains(out, `"`+id+`"`)
	}

	// A job that exits 0 lets the app start.
	ok := `services:
  migrate:
    image: test/app:1
    command: ["true"]
    restart: "no"
  app:
    image: test/app:1
    command: [sleep, "300"]
    depends_on:
      migrate: {condition: service_completed_successfully}
`
	if code, out := up("dep", ok, nil); code != 200 {
		t.Fatalf("completed dependency: %d %s", code, out)
	}
	if !running("dep-app") {
		t.Error("dep-app not running after its dependency completed")
	}
	d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": ok, "context_dir": dir, "project": "dep"})

	// A job that fails stops the rest of the stack from starting. It runs a
	// moment first: an immediate failure already fails the job's own start.
	failing := strings.Replace(ok, `command: ["true"]`, `command: [sh, -c, "sleep 1; exit 3"]`, 1)
	code, out := up("depfail", failing, nil)
	if code != 400 || !strings.Contains(out, "dependency depfail-migrate: exited with code 3") {
		t.Errorf("failed dependency: %d %s", code, out)
	}
	if running("depfail-app") {
		t.Error("depfail-app started although its dependency failed")
	}
	d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": failing, "context_dir": dir, "project": "depfail"})

	// service_healthy without a healthcheck is refused up front.
	noHC := `services:
  db: {image: test/app:1, command: [sleep, "300"]}
  app:
    image: test/app:1
    depends_on:
      db: {condition: service_healthy}
`
	if code, out := up("dephc", noHC, nil); code != 400 || !strings.Contains(out, `"db" has no healthcheck`) {
		t.Errorf("service_healthy without healthcheck: %d %s", code, out)
	}
	if code, out := up("dept", ok, map[string]any{"wait_timeout": "soon"}); code != 400 || !strings.Contains(out, "invalid wait_timeout") {
		t.Errorf("bad wait_timeout: %d %s", code, out)
	}
}
