package compose_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/pkg/oci"
)

func storeWithImage(t *testing.T) *image.Store {
	t.Helper()
	s, err := image.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: runtime.GOARCH, Config: oci.ContainerConfig{Cmd: []string{"sh"}}}
	if err := daemon.StoreImage(s, "test/app:1", cfg, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBuildContainerSpecs(t *testing.T) {
	ctx := context.Background()
	s := storeWithImage(t)
	dir := t.TempDir()
	if _, err := compose.BuildContainerSpecs(ctx, &compose.Stack{}, compose.UpMeta{}, nil, "native", "auto"); err == nil {
		t.Error("nil store")
	}
	full := `
services:
  db:
    image: test/app:1
    user: "1000:1000"
    environment: {A: "1"}
    ports: ["5432:5432"]
    restart: always
    read_only: true
    privileged: true
    cap_add: [NET_ADMIN]
    cap_drop: [ALL]
    seccomp_profile: profile.json
    stop_timeout: 3s
    healthcheck:
      test: ["CMD", "true"]
    deploy:
      resources:
        limits: {memory: 64m, cpus: "0.5"}
    networks: [back]
    registry_username: u
    registry_password: p
  web:
    image: test/app:1
    depends_on: [db]
    networks: [front]
networks:
  back: {internal: true}
  front: {}
`
	st, err := compose.ParseInterpolated([]byte(full), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ driver, dns string }{{"native", "auto"}, {"native", "embedded"}, {"cni", "auto"}, {"cni", ""}, {"host", "off"}} {
		specs, err := compose.BuildContainerSpecs(ctx, st, compose.UpMeta{ComposeDir: dir, Project: "p", DataDir: t.TempDir()}, s, c.driver, c.dns)
		if err != nil || len(specs) != 2 || specs[0].ID != "p-db" {
			t.Fatalf("%+v: %v %+v", c, err, specs)
		}
		db := specs[0]
		if !db.ComposeInternalNet || db.User == nil || db.Resources == nil || db.Healthcheck == nil || len(db.PortMappings) != 1 {
			t.Errorf("%+v: db spec %+v", c, db)
		}
	}
	for want, svc := range map[string]string{
		"pull":        "image: registry.invalid/x:1",
		"volumes":     "image: test/app:1\n    volumes: [':']",
		"ports":       "image: test/app:1\n    ports: [nope]",
		"healthcheck": "image: test/app:1\n    healthcheck: {test: [CMD]}",
		"user":        "image: test/app:1\n    user: root",
		"resources":   "image: test/app:1\n    deploy: {resources: {limits: {memory: lots}}}",
	} {
		st, err := compose.ParseInterpolated([]byte("services:\n  s:\n    "+svc+"\n"), dir)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if _, err := compose.BuildContainerSpecs(ctx, st, compose.UpMeta{ComposeDir: dir, Project: "p", DataDir: t.TempDir()}, s, "host", "auto"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	// Implicit networks: "default", or the only declared one.
	for src, want := range map[string]bool{
		"networks: {default: {internal: true}, other: {}}": true,
		"networks: {only: {internal: true}}":               true,
		"networks: {a: {}, b: {internal: true}}":           false,
	} {
		st, _ := compose.ParseInterpolated([]byte("services:\n  s:\n    image: test/app:1\n"+src+"\n"), dir)
		if got := compose.ServiceUsesOnlyInternalNetworks(st, st.Services["s"]); got != want {
			t.Errorf("%s: %v", src, got)
		}
	}
	if compose.ServiceUsesOnlyInternalNetworks(&compose.Stack{Networks: map[string]compose.Network{"x": {Internal: true}}}, compose.Service{Networks: []string{"y"}}) {
		t.Error("undeclared network")
	}
}
