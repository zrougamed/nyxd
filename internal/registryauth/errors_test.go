package registryauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
)

func TestLookupSourcesAndErrors(t *testing.T) {
	d := isolate(t)
	// REGISTRY_AUTH_FILE (podman), then a docker file with a plain
	// username/password entry and one keyed by URL.
	t.Setenv("REGISTRY_AUTH_FILE", filepath.Join(d, "podman.json"))
	write(t, filepath.Join(d, "podman.json"), `{"auths":{"quay.io":{"username":"pu","password":"pp"}}}`)
	write(t, filepath.Join(d, ".docker/config.json"), `{"auths":{"https://ghcr.io/v2/":{"auth":"`+b64("gu:gp")+`"},"empty.example":{}}}`)
	if a, from, ok, err := Lookup("quay.io"); !ok || err != nil || a.Username != "pu" || from != filepath.Join(d, "podman.json") {
		t.Errorf("podman: %+v %s %v %v", a, from, ok, err)
	}
	if a, _, ok, err := Lookup("ghcr.io"); !ok || err != nil || a.Password != "gp" {
		t.Errorf("URL key: %+v %v %v", a, ok, err)
	}
	if _, _, ok, err := Lookup("empty.example"); ok || err != nil {
		t.Errorf("empty marker: %v %v", ok, err)
	}

	// A broken file is reported and skipped; For collects per registry once.
	write(t, filepath.Join(d, "podman.json"), `{`)
	m, err := For("ghcr.io/a:1", "ghcr.io/b:1", "quay.io/c:1")
	if err == nil || len(m) != 1 || m["ghcr.io"].Username != "gu" {
		t.Errorf("For: %v %v", m, err)
	}
	os.Remove(filepath.Join(d, "podman.json"))
	os.Mkdir(filepath.Join(d, "podman.json"), 0o700) //nolint:errcheck
	if _, _, _, err := Lookup("quay.io"); err == nil {
		t.Error("unreadable auth file not reported")
	}
}

func TestHelperFailures(t *testing.T) {
	d := isolate(t)
	helper := func(name, script string) {
		p := filepath.Join(d, "bin", "docker-credential-"+name)
		write(t, p, "#!/bin/sh\n"+script)
		os.Chmod(p, 0o755) //nolint:errcheck
	}
	helper("crash", "echo boom >&2; exit 3\n")
	helper("garbage", "echo not-json\n")
	write(t, filepath.Join(d, ".docker/config.json"), `{"credHelpers":{"a.example":"crash","b.example":"garbage"}}`)
	if _, _, _, err := Lookup("a.example"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("crashing helper: %v", err)
	}
	if _, _, _, err := Lookup("b.example"); err == nil || !strings.Contains(err.Error(), "docker-credential-garbage") {
		t.Errorf("garbage helper: %v", err)
	}
}

func TestEditErrors(t *testing.T) {
	d := t.TempDir()
	a := image.RegistryAuth{Username: "u", Password: "p"}
	bad := filepath.Join(d, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o600) //nolint:errcheck
	if err := Save(bad, "x.io", a); err == nil {
		t.Error("save into a broken file")
	}
	badAuths := filepath.Join(d, "auths.json")
	os.WriteFile(badAuths, []byte(`{"auths":[1]}`), 0o600) //nolint:errcheck
	if _, err := Remove(badAuths, "x.io"); err == nil || !strings.Contains(err.Error(), "auths") {
		t.Errorf("auths not an object: %v", err)
	}
	if err := Save(d, "x.io", a); err == nil {
		t.Error("save onto a directory")
	}
	file := filepath.Join(d, "file")
	os.WriteFile(file, nil, 0o600) //nolint:errcheck
	if err := Save(filepath.Join(file, "auth.json"), "x.io", a); err == nil {
		t.Error("save under a file")
	}
	ro := filepath.Join(d, "ro")
	os.Mkdir(ro, 0o500) //nolint:errcheck
	if os.Getuid() != 0 {
		if err := Save(filepath.Join(ro, "auth.json"), "x.io", a); err == nil {
			t.Error("save into a read-only directory")
		}
	}
}
