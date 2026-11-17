package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposePull(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(`services:
  db: {image: "postgres:17"}
  cache: {image: "redis:7"}
  broken: {image: "nope:1"}
  app: {build: ., image: "me/app:dev"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var refs []string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		body := buf.String()
		w.Header().Set("Content-Type", "application/x-ndjson")
		switch {
		case strings.Contains(body, "postgres:17"):
			refs = append(refs, "postgres")
			_, _ = w.Write([]byte(`{"phase":"layer","digest":"sha256:a","cached":true}` + "\n" + `{"phase":"done","ok":true}` + "\n"))
		case strings.Contains(body, "redis:7"):
			refs = append(refs, "redis")
			_, _ = w.Write([]byte(`{"phase":"layer","digest":"sha256:b"}` + "\n" + `{"phase":"layer","digest":"sha256:c"}` + "\n" + `{"phase":"done","ok":true}` + "\n"))
		case strings.Contains(body, "me/app:dev"):
			refs = append(refs, "app")
			_, _ = w.Write([]byte(`{"phase":"done","ok":true}` + "\n"))
		default:
			_, _ = w.Write([]byte(`{"phase":"error","message":"manifest unknown"}` + "\n"))
		}
	})
	_, stderr, err := capture(t, "", func() error { return run([]string{"-socket", sock, "compose", "pull", "-f", file}) })
	if err == nil || !strings.Contains(err.Error(), "compose pull failed for: broken") {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{"db (postgres:17): up to date", "cache (redis:7): pulled, 2 new layer(s)", "broken (nope:1): manifest unknown", "app: skipped (build:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
	refs = nil
	_, stderr, err = capture(t, "", func() error {
		return run([]string{"-socket", sock, "compose", "pull", "-f", file, "--include-build", "app", "db"})
	})
	if err != nil || len(refs) != 2 || !strings.Contains(stderr, "app (me/app:dev): up to date") {
		t.Fatalf("selected services: %v %v\n%s", err, refs, stderr)
	}
	for _, args := range [][]string{
		{"compose", "pull", "-f", file, "nosuch"},
		{"compose", "pull", "-f", file, "--build"},
		{"compose", "up", "-f", file, "--include-build"},
		{"compose", "up", "-f", file, "--force-recreate", "--no-recreate"},
		{"compose", "down", "-f", file, "--no-recreate"},
		{"compose", "stop", "-f", file, "--include-build"},
	} {
		if err := run(append([]string{"-socket", sock}, args...)); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	// A stream that ends without "done" is an error.
	short := stub(t, reply(200, `{"phase":"layer","digest":"sha256:a"}`+"\n"+`{"phase":"layer","digest":"sha256:b"}`+"\n"))
	if _, err := pullQuiet(short, "x:1", nil); err == nil {
		t.Error("truncated stream accepted")
	}
}

func TestPrintComposeResults(t *testing.T) {
	var b strings.Builder
	printComposeResults(&b, []any{
		map[string]any{"id": "p-db", "service": "db", "action": "recreated", "reason": "image changed"},
		map[string]any{"id": "p-web", "action": "up to date"},
		"garbage",
	})
	if b.String() != "db: recreated (image changed)\np-web: up to date\n" {
		t.Fatalf("%q", b.String())
	}
}
