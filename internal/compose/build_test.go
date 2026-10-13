package compose

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBuild(t *testing.T) {
	t.Setenv("FROM_ENV", "env-value")
	st, err := Parse([]byte(`
services:
  short:
    build: ./app
  long:
    image: registry.example/team/long:2
    build:
      context: ../src
      dockerfile: docker/Dockerfile.prod
      target: runtime
      args:
        A: "1"
        FROM_ENV:
  listargs:
    build:
      args: ["B=2", "FROM_ENV", "UNSET_ARG_XYZ"]
  pulled:
    image: alpine:3.20
`))
	if err != nil {
		t.Fatal(err)
	}
	st.SetBuildImages("MyProj")

	short := st.Services["short"]
	if short.Build == nil || short.Build.Context != "./app" {
		t.Fatalf("short build: %+v", short.Build)
	}
	if short.Image != "myproj-short:latest" {
		t.Errorf("default image = %q", short.Image)
	}
	ctx, err := short.Build.ContextDir("/srv/stack")
	if err != nil || ctx != "/srv/stack/app" {
		t.Errorf("context = %q, %v", ctx, err)
	}
	if df := short.Build.DockerfilePath(ctx); df != "/srv/stack/app/Dockerfile" {
		t.Errorf("dockerfile = %q", df)
	}

	long := st.Services["long"]
	if long.Image != "registry.example/team/long:2" {
		t.Errorf("explicit image replaced: %q", long.Image)
	}
	ctx, _ = long.Build.ContextDir("/srv/stack")
	if ctx != "/srv/src" || long.Build.DockerfilePath(ctx) != filepath.Join("/srv/src", "docker/Dockerfile.prod") || long.Build.Target != "runtime" {
		t.Errorf("long build: %+v ctx %q", long.Build, ctx)
	}
	if a := long.Build.Args; a["A"] != "1" || a["FROM_ENV"] != "env-value" || len(a) != 2 {
		t.Errorf("mapping args: %v", a)
	}

	la := st.Services["listargs"].Build
	if la.Context != "" || la.Args["B"] != "2" || la.Args["FROM_ENV"] != "env-value" || len(la.Args) != 2 {
		t.Errorf("list args: %+v", la)
	}
	if ctx, _ := la.ContextDir("/srv/stack"); ctx != "/srv/stack" {
		t.Errorf("empty context = %q", ctx)
	}

	if got := strings.Join(st.BuildServices(), ","); got != "listargs,long,short" {
		t.Errorf("BuildServices = %s", got)
	}
}

func TestParseBuildErrors(t *testing.T) {
	if _, err := Parse([]byte("services:\n  x:\n    restart: always\n")); err == nil || !strings.Contains(err.Error(), "missing image (or build)") {
		t.Errorf("no image, no build: %v", err)
	}
	if _, err := Parse([]byte("services:\n  x:\n    build: [a]\n")); err == nil {
		t.Error("sequence build accepted")
	}
	if _, err := Parse([]byte("services:\n  x:\n    build:\n      args: 3\n")); err == nil {
		t.Error("scalar args accepted")
	}
	for _, c := range []string{"https://github.com/x/y.git", "git@github.com:x/y.git"} {
		if _, err := (&Build{Context: c}).ContextDir("/srv"); err == nil {
			t.Errorf("remote context %q accepted", c)
		}
	}
}
