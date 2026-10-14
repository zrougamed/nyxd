package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// composeDaemon stands in for nyxd during compose up: images whose ref
// contains "have" exist, "broken" makes inspect fail, and builds answer
// buildReply. It records the build queries and compose bodies.
type composeDaemon struct {
	mu         sync.Mutex
	builds     []url.Values
	bodies     []map[string]any
	buildReply string
}

func (d *composeDaemon) handler() http.HandlerFunc {
	return routes(map[string]http.HandlerFunc{
		"/images/inspect": func(w http.ResponseWriter, r *http.Request) {
			ref := r.URL.Query().Get("ref")
			switch {
			case strings.Contains(ref, "have"):
				io.WriteString(w, `{}`) //nolint:errcheck
			case strings.Contains(ref, "broken"):
				http.Error(w, "store unreadable", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		},
		"/build": func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body) //nolint:errcheck
			d.mu.Lock()
			d.builds = append(d.builds, r.URL.Query())
			d.mu.Unlock()
			io.WriteString(w, d.buildReply) //nolint:errcheck
		},
		"/compose/up": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			d.mu.Lock()
			d.bodies = append(d.bodies, body)
			d.mu.Unlock()
			io.WriteString(w, `{"ok":true,"ids":["p-app","p-web"],"warnings":["network x is internal",7]}`) //nolint:errcheck
		},
		"/compose/stop": reply(200, `{"ok":true,"stopped":["p-app"]}`),
	})
}

const buildDone = "{\"stream\":\"Step 1/1\\n\"}\n{\"done\":true,\"image_id\":\"sha256:0123456789abcdef\",\"tags\":[\"t\"]}\n"

func writeProject(t *testing.T, compose string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)                                                       //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "app", "Dockerfile.dev"), []byte("FROM scratch AS final\n"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "app", "Dockerfile"), []byte("FROM scratch\n"), 0o644)              //nolint:errcheck
	p := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(p, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComposeUpBuilds(t *testing.T) {
	t.Setenv("NYX_AUTH_FILE", filepath.Join(t.TempDir(), "none.json"))
	f := writeProject(t, `name: p
services:
  app:
    build:
      context: ./app
      dockerfile: Dockerfile.dev
      target: final
      args:
        B: "2"
        A: "1"
  have:
    image: have:1
    build: ./app
  web:
    image: nginx
`)
	d := &composeDaemon{buildReply: buildDone}
	sock := stub(t, d.handler())

	out, errOut, err := nyx(t, "-socket", sock, "compose", "up", "-f", f)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(d.builds) != 1 {
		t.Fatalf("builds = %v, want only app (have:1 exists)", d.builds)
	}
	q := d.builds[0]
	if q.Get("t") != "p-app:latest" || q.Get("target") != "final" || strings.Join(q["buildarg"], ",") != "A=1,B=2" {
		t.Errorf("build query %v", q)
	}
	if !strings.Contains(errOut, "Building app (p-app:latest)") || !strings.Contains(errOut, "warning: network x is internal") ||
		!strings.Contains(errOut, "Started 2 container(s): p-app, p-web") || !strings.Contains(out, "p-web") {
		t.Errorf("output:\n%s\n%s", out, errOut)
	}
	if d.bodies[0]["file"] != f {
		t.Errorf("compose up body %v", d.bodies[0])
	}

	// --build rebuilds what exists; --project names the images.
	d.builds = nil
	if _, errOut, err := nyx(t, "-socket", sock, "compose", "up", "--build", "--project=q", "--file="+f); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(d.builds) != 2 || d.bodies[1]["project"] != "q" {
		t.Errorf("--build: builds %v, body %v", d.builds, d.bodies[1])
	}
	out, _, err = nyx(t, "-socket", sock, "compose", "stop", "-f="+f, "--project", "q")
	if err != nil || !strings.Contains(out, "p-app") {
		t.Errorf("stop: %v %s", err, out)
	}
}

func TestComposeUpBuildErrors(t *testing.T) {
	t.Setenv("NYX_AUTH_FILE", filepath.Join(t.TempDir(), "none.json"))
	for _, c := range []struct {
		name, compose, reply, want string
	}{
		{"inspect fails", "services:\n  s:\n    image: broken:1\n    build: ./app\n", buildDone, "store unreadable"},
		{"build fails", "services:\n  s:\n    build: ./app\n", "{\"error\":\"RUN failed\"}\n", `service "s": build failed: RUN failed`},
		{"no context dir", "services:\n  s:\n    build: ./nope\n", buildDone, "is not a directory"},
		{"remote context", "services:\n  s:\n    build: https://example.com/repo.git\n", buildDone, "only local directories"},
		{"bad yaml", "services: [\n", buildDone, "compose"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := writeProject(t, c.compose)
			sock := stub(t, (&composeDaemon{buildReply: c.reply}).handler())
			_, errOut, err := nyx(t, "-socket", sock, "compose", "up", "-f", f)
			if err == nil || !strings.Contains(err.Error()+errOut, c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestComposeArgErrors(t *testing.T) {
	sock := stub(t, reply(500, "unused"))
	f := writeCompose(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"up", "-f"}, "requires a path"},
		{[]string{"up", "--project"}, "requires a value"},
		{[]string{"up", "--bogus"}, "unknown flag"},
		{[]string{"up", "extra"}, "unexpected argument"},
		{[]string{"up", "-v", "-f", f}, "does not take -v"},
		{[]string{"stop", "--build", "-f", f}, "only for compose up"},
		{[]string{"restart", "-f", f}, "unknown compose subcommand"},
		{[]string{"stop"}, "compose"},
		{[]string{"down"}, "compose"},
		{[]string{"up"}, "compose"},
	} {
		// No compose file in the test's working directory.
		_, errOut, err := nyx(t, append([]string{"-socket", sock, "compose"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error()+errOut, c.want) {
			t.Errorf("compose %v: err %v, want %q", c.args, err, c.want)
		}
	}
}
