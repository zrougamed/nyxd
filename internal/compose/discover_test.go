package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultComposePath_priority(t *testing.T) {
	dir := t.TempDir()
	// Lower-priority file present first — should still pick nyx-compose.yaml when added?
	// Create docker-compose first then nyx — order lists nyx first.
	docker := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(docker, []byte("version: '3'\nservices:\n  a:\n    image: alpine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultComposePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != docker {
		t.Fatalf("expected %q got %q", docker, got)
	}
	nyx := filepath.Join(dir, "nyx-compose.yaml")
	if err := os.WriteFile(nyx, []byte("version: '3'\nservices:\n  b:\n    image: alpine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got2, err := DefaultComposePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != nyx {
		t.Fatalf("nyx-compose should win: want %q got %q", nyx, got2)
	}
}

func TestDefaultProjectFromComposeFile(t *testing.T) {
	sub := filepath.Join(t.TempDir(), "acme-api", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(sub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := DefaultProjectFromComposeFile(sub); g != "acme-api" {
		t.Fatalf("parent dir name: want acme-api got %q", g)
	}
	// Same filename in a different folder → different project label.
	other := filepath.Join(t.TempDir(), "billing", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := DefaultProjectFromComposeFile(other); g != "billing" {
		t.Fatalf("want billing got %q", g)
	}
	if DefaultProjectFromComposeFile(sub) == DefaultProjectFromComposeFile(other) {
		t.Fatal("expected distinct default projects for two docker-compose.yml paths")
	}
	if filepath.Separator == '/' {
		if g := DefaultProjectFromComposeFile("/docker-compose.yml"); g != "docker-compose" {
			t.Fatalf("root compose file fallback: want docker-compose got %q", g)
		}
	}
}
