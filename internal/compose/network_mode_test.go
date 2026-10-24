package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/compose"
)

func TestServiceUsesHostNetwork(t *testing.T) {
	st, err := compose.Parse([]byte(`
services:
  a:
    image: x
    network_mode: host
  b:
    image: x
    networks: [hn]
  c:
    image: x
networks:
  hn:
    driver: host
`))
	if err != nil {
		t.Fatal(err)
	}
	if !compose.ServiceUsesHostNetwork(st, st.Services["a"]) {
		t.Error("network_mode host")
	}
	if !compose.ServiceUsesHostNetwork(st, st.Services["b"]) {
		t.Error("driver host network")
	}
	if compose.ServiceUsesHostNetwork(st, st.Services["c"]) {
		t.Error("default should not be host")
	}
	if !compose.StackUsesHostNetwork(st) {
		t.Error("stack should use host")
	}
	if compose.StackUsesHostNetwork(&compose.Stack{Services: map[string]compose.Service{"x": {Image: "x"}}}) {
		t.Error("plain stack")
	}
}

func TestParseNetworkMode(t *testing.T) {
	if _, err := compose.Parse([]byte("services:\n  s:\n    image: x\n    network_mode: host\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := compose.Parse([]byte("services:\n  s:\n    image: x\n    network_mode: container:other\n")); err == nil || !strings.Contains(err.Error(), "unsupported network_mode") {
		t.Fatalf("want unsupported: %v", err)
	}
}

func TestBuildContainerSpecsHostNetwork(t *testing.T) {
	ctx := context.Background()
	s := storeWithImage(t)
	dir := t.TempDir()
	meta := compose.UpMeta{ComposeDir: dir, Project: "demo", DataDir: t.TempDir()}

	st, err := compose.ParseInterpolated([]byte(`
services:
  db:
    image: test/app:1
    network_mode: host
  web:
    image: test/app:1
    network_mode: host
    depends_on: [db]
`), dir)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := compose.BuildContainerSpecs(ctx, st, meta, s, "native", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("len=%d", len(specs))
	}
	for _, sp := range specs {
		if !sp.HostNetwork {
			t.Errorf("%s: HostNetwork=false", sp.ID)
		}
		if len(sp.PortMappings) != 0 {
			t.Errorf("%s: unexpected ports", sp.ID)
		}
		joined := strings.Join(sp.HostAliases, " ")
		if !strings.Contains(joined, "db") || !strings.Contains(joined, "web") || !strings.Contains(joined, "demo-db") {
			t.Errorf("%s aliases=%v", sp.ID, sp.HostAliases)
		}
	}

	stBad, err := compose.ParseInterpolated([]byte(`
services:
  s:
    image: test/app:1
    network_mode: host
    ports: ["8080:80"]
`), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compose.BuildContainerSpecs(ctx, stBad, meta, s, "native", "auto"); err == nil || !strings.Contains(err.Error(), "published ports cannot be used with network host") {
		t.Fatalf("ports+host: %v", err)
	}

	stDrv, err := compose.ParseInterpolated([]byte(`
services:
  s:
    image: test/app:1
    networks: [h]
networks:
  h:
    driver: host
`), dir)
	if err != nil {
		t.Fatal(err)
	}
	specs, err = compose.BuildContainerSpecs(ctx, stDrv, meta, s, "native", "auto")
	if err != nil || len(specs) != 1 || !specs[0].HostNetwork {
		t.Fatalf("driver host: %+v %v", specs, err)
	}
}
