package registryauth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
)

// isolate points every lookup location into a temp dir.
func isolate(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(d, "config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(d, "run"))
	t.Setenv("NYX_AUTH_FILE", "")
	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("PATH", filepath.Join(d, "bin"))
	return d
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestLookupDockerPodmanNyx(t *testing.T) {
	d := isolate(t)
	write(t, filepath.Join(d, ".docker/config.json"), `{"auths":{
		"https://index.docker.io/v1/":{"auth":"`+b64("dockeruser:dockerpass")+`"},
		"ghcr.io":{"auth":"`+b64("gh:docker")+`"}}}`)
	write(t, filepath.Join(d, "run/containers/auth.json"), `{"auths":{
		"quay.io/myorg":{"auth":"`+b64("quser:qpass")+`"},
		"ghcr.io":{"auth":"`+b64("gh:podman")+`"}}}`)

	cases := []struct{ host, user, pass, from string }{
		{"docker.io", "dockeruser", "dockerpass", ".docker/config.json"},
		{"registry-1.docker.io", "dockeruser", "dockerpass", ".docker/config.json"},
		{"quay.io", "quser", "qpass", "run/containers/auth.json"},
		{"ghcr.io", "gh", "podman", "run/containers/auth.json"}, // podman before docker
	}
	for _, c := range cases {
		a, from, ok, err := Lookup(c.host)
		if err != nil || !ok || a.Username != c.user || a.Password != c.pass || !strings.HasSuffix(from, c.from) {
			t.Errorf("%s: %+v from %q ok=%v err=%v", c.host, a, from, ok, err)
		}
	}
	if _, _, ok, err := Lookup("example.com"); ok || err != nil {
		t.Errorf("unknown host: ok=%v err=%v", ok, err)
	}

	// nyx login wins over both.
	nyx, _ := NyxFile()
	if err := Save(nyx, "https://ghcr.io/", image.RegistryAuth{Username: "gh", Password: "nyx"}); err != nil {
		t.Fatal(err)
	}
	if a, from, _, _ := Lookup("ghcr.io"); a.Password != "nyx" || from != nyx {
		t.Errorf("nyx file: %+v from %s", a, from)
	}
	if st, _ := os.Stat(nyx); st.Mode().Perm() != 0o600 {
		t.Errorf("auth file mode %v", st.Mode().Perm())
	}
	m, err := For("ghcr.io/me/app:1", "alpine", "quay.io/myorg/x", "example.com/y")
	if err != nil || len(m) != 3 || m.For("ghcr.io/me/other").Password != "nyx" || m.For("busybox").Username != "dockeruser" || m.For("example.com/y") != nil {
		t.Errorf("For: %v %v", m, err)
	}
}

func TestCredentialHelpers(t *testing.T) {
	d := isolate(t)
	// A fake docker-credential-test that knows one server URL.
	write(t, filepath.Join(d, "bin/docker-credential-test"), `#!/bin/sh
read url
case "$url" in
  https://index.docker.io/v1/) echo '{"ServerURL":"x","Username":"hub","Secret":"s3cret"}' ;;
  registry.example) echo '{"ServerURL":"x","Username":"<token>","Secret":"refresh"}' ;;
  *) echo "credentials not found in native keychain"; exit 1 ;;
esac
`)
	if err := os.Chmod(filepath.Join(d, "bin/docker-credential-test"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d, ".docker/config.json"), `{"auths":{"https://index.docker.io/v1/":{}},
		"credsStore":"test","credHelpers":{"broken.example":"missing","bad.example":"../x"}}`)

	if a, _, ok, err := Lookup("docker.io"); !ok || err != nil || a.Username != "hub" || a.Password != "s3cret" {
		t.Errorf("credsStore: %+v ok=%v err=%v", a, ok, err)
	}
	if _, _, ok, err := Lookup("ghcr.io"); ok || err != nil {
		t.Errorf("not in store: ok=%v err=%v", ok, err)
	}
	if _, _, ok, _ := Lookup("registry.example"); ok {
		t.Error("identity token used as a password")
	}
	if _, _, ok, err := Lookup("broken.example"); ok || err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("missing helper: ok=%v err=%v", ok, err)
	}
	if _, _, _, err := Lookup("bad.example"); err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Errorf("path in helper name: %v", err)
	}
}

func TestSaveRemoveKeepOtherFields(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "auth.json")
	write(t, p, `{"auths":{"https://index.docker.io/v1/":{"auth":"`+b64("old:old")+`"},"quay.io":{"auth":"`+b64("q:q")+`"}},"credHelpers":{"x.io":"y"},"other":1}`)
	if err := Save(p, "docker.io", image.RegistryAuth{Username: "new", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Auths       map[string]authEntry `json:"auths"`
		CredHelpers map[string]string    `json:"credHelpers"`
		Other       int                  `json:"other"`
	}
	data, _ := os.ReadFile(p)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Auths) != 2 || got.Auths["docker.io"].Auth != b64("new:pw") || got.CredHelpers["x.io"] != "y" || got.Other != 1 {
		t.Errorf("after save: %s", data)
	}
	if ok, err := Remove(p, "index.docker.io"); !ok || err != nil {
		t.Errorf("remove: %v %v", ok, err)
	}
	if ok, _ := Remove(p, "docker.io"); ok {
		t.Error("removed twice")
	}
	data, _ = os.ReadFile(p)
	if strings.Contains(string(data), "docker.io") || !strings.Contains(string(data), "quay.io") {
		t.Errorf("after remove: %s", data)
	}
}
