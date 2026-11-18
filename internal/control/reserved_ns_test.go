//go:build linux

package control_test

import (
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestControlReservedContainerNames(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"id": "_layers", "image": "test/app:1", "args": []string{"sleep", "1"}})
	if code != 400 || !strings.Contains(out, "reserved") {
		t.Errorf("run _layers: %d %s", code, out)
	}
	// A compose service in the default project gets its bare name as id.
	yaml := "services:\n  _layers:\n    image: test/app:1\n    command: [sleep, '1']\n"
	code, out = d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": yaml, "context_dir": t.TempDir(), "project": "default"})
	if code != 400 || !strings.Contains(out, "reserved") {
		t.Errorf("compose service _layers: %d %s", code, out)
	}
}
