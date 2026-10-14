package compose_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestBuildContainerSpecsMore(t *testing.T) {
	ctx := context.Background()
	s := storeWithImage(t)
	// An image with neither entrypoint nor command.
	if err := daemon.StoreImage(s, "bare/app:1", &oci.ImageConfig{OS: "linux", Architecture: runtime.GOARCH}, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	meta := compose.UpMeta{ComposeDir: dir, Project: "p", DataDir: t.TempDir()}
	parse := func(svc string) *compose.Stack {
		st, err := compose.ParseInterpolated([]byte("services:\n  s:\n    "+svc+"\n"), dir)
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		return st
	}

	for want, svc := range map[string]string{
		"is not built yet": "build: .\n    image: absent/app:1",
		"missing.env":      "image: test/app:1\n    env_file: missing.env",
	} {
		if _, err := compose.BuildContainerSpecs(ctx, parse(svc), meta, s, "host", "auto"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	specs, err := compose.BuildContainerSpecs(ctx, parse("image: bare/app:1\n    networks: [n]\nnetworks:\n  n: {}"), meta, s, "cni", "embedded")
	if err != nil || len(specs) != 1 || !specs[0].EmbedDNS || len(specs[0].Args) != 0 {
		t.Errorf("embedded DNS, no command: %+v %v", specs, err)
	}
}

// A dependency cycle (Parse rejects one) still yields every service once.
func TestTopologicalOrderCycle(t *testing.T) {
	st := &compose.Stack{Services: map[string]compose.Service{
		"a": {DependsOn: compose.DependsOn{"b"}},
		"b": {DependsOn: compose.DependsOn{"a"}},
		"c": {},
	}}
	if got := strings.Join(compose.TopologicalOrder(st), ","); got != "c,a,b" {
		t.Errorf("order = %s", got)
	}
}
