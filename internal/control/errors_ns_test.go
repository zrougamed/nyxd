//go:build linux

package control_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlKillAndComposeErrors(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)

	// crun refuses a signal it does not know: the container keeps running.
	run(t, d, map[string]any{"id": "k1", "args": []string{"sleep", "300"}, "restart": "no"})
	if code, out := d.Do(t, "POST", "/v1/containers/k1/kill", map[string]any{"signal": "SIGBOGUS"}); code != 400 {
		t.Errorf("bogus signal: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/k1/kill", map[string]any{"signal": "TERM"}); code != 200 {
		t.Errorf("kill: %d %s", code, out)
	}
	// Killing a container that already exited is not an error.
	run(t, d, map[string]any{"id": "k2", "args": []string{"true"}, "restart": "no"})
	daemon.WaitFor(t, 15*time.Second, "k2 gone", func() bool {
		_, out := d.Do(t, "GET", "/v1/containers", nil)
		return !strings.Contains(out, `"k2"`)
	})
	if code, out := d.Do(t, "POST", "/v1/containers/k2/kill", nil); code != 404 {
		t.Errorf("kill of a removed container: %d %s", code, out)
	}

	dir := t.TempDir()
	internal := "networks:\n  back:\n    internal: true\nservices:\n  a:\n    image: test/app:1\n    command: [sleep, '300']\n    networks: [back]\n"
	code, out := d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": internal, "context_dir": dir, "project": "warn"})
	if code != 200 || !strings.Contains(out, "internal networks are not isolated") {
		t.Errorf("internal network on the host network: %d %s", code, out)
	}
	// A failing stack still reports the warnings.
	failing := "networks:\n  back:\n    internal: true\nservices:\n  a:\n    image: test/app:1\n    command: [/no/such/binary]\n    networks: [back]\n"
	code, out = d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": failing, "context_dir": dir, "project": "warnfail"})
	if code != 400 || !strings.Contains(out, "warning: internal networks") {
		t.Errorf("failing stack: %d %s", code, out)
	}
	for _, p := range []string{"/v1/compose/stop", "/v1/compose/down"} {
		if code, out := d.Do(t, "POST", p, map[string]any{"yaml": "services: [", "context_dir": dir}); code != 400 {
			t.Errorf("%s with bad yaml: %d %s", p, code, out)
		}
	}
	d.Do(t, "POST", "/v1/compose/down", map[string]any{"yaml": internal, "context_dir": dir, "project": "warn"})
}
