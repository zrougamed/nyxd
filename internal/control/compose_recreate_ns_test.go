//go:build linux

package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlComposeUpRecreate(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	stack := func(mode string) string {
		return `services:
  web:
    image: test/app:1
    command: [sleep, "300"]
  db:
    image: test/app:1
    command: [sleep, "300"]
    environment: {MODE: ` + mode + `}
`
	}
	up := func(yaml, recreate string) map[string]string {
		t.Helper()
		body := map[string]any{"yaml": yaml, "context_dir": dir, "project": "rc"}
		if recreate != "" {
			body["recreate"] = recreate
		}
		code, out := d.Do(t, "POST", "/v1/compose/up", body)
		if code != 200 {
			t.Fatalf("up %s: %d %s", recreate, code, out)
		}
		var r struct {
			Services []struct{ Service, Action, Reason string } `json:"services"`
		}
		_ = json.Unmarshal([]byte(out), &r)
		got := map[string]string{}
		for _, s := range r.Services {
			got[s.Service] = strings.TrimSpace(s.Action + " " + s.Reason)
		}
		return got
	}
	if got := up(stack("one"), ""); got["web"] != "started" || got["db"] != "started" {
		t.Fatalf("first up: %v", got)
	}
	if got := up(stack("one"), ""); got["web"] != "up to date" || got["db"] != "up to date" {
		t.Errorf("no-op up: %v", got)
	}
	if got := up(stack("two"), ""); got["db"] != "recreated config changed" || got["web"] != "up to date" {
		t.Errorf("changed env: %v", got)
	}
	if got := up(stack("three"), "never"); got["db"] != "kept config changed, --no-recreate" {
		t.Errorf("--no-recreate: %v", got)
	}
	if got := up(stack("three"), "force"); got["db"] != "recreated --force-recreate" || got["web"] != "recreated --force-recreate" {
		t.Errorf("--force-recreate: %v", got)
	}
	if code, out := d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": stack("x"), "context_dir": dir, "project": "rc", "recreate": "sometimes"}); code != 400 || !strings.Contains(out, "invalid recreate") {
		t.Errorf("bad recreate: %d %s", code, out)
	}
	d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": stack("x"), "context_dir": dir, "project": "rc"})
}
