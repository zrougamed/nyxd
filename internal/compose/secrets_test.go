package compose

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const secretsStack = `
services:
  db:
    image: postgres:17
    secrets:
      - db_password
      - source: api_key
        target: keys/api
        uid: "UID"
        gid: "GID"
        mode: 0440
    configs:
      - pg_conf
      - source: tuned
        target: /etc/tuned.conf
        mode: 0o444
secrets:
  db_password: {file: ./db_password.txt}
  api_key: {file: ./api_key.txt}
  from_env: {environment: DB_PASS}
  ext: {external: true}
configs:
  pg_conf: {file: ./postgresql.conf}
  tuned: {file: ./tuned.conf}
`

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecretMounts(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFiles(t, dir, map[string]string{"db_password.txt": "hunter2", "api_key.txt": "k-123", "postgresql.conf": "max_connections=10", "tuned.conf": "x=1"})
	yaml := strings.NewReplacer("UID", itoa(os.Getuid()), "GID", itoa(os.Getgid())).Replace(secretsStack)
	st, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	svc := st.Services["db"]
	if svc.Secrets[1].Target != "keys/api" || *svc.Secrets[1].Mode != 0o440 || *svc.Configs[1].Mode != 0o444 {
		t.Fatalf("parsed: %+v %+v", svc.Secrets[1], svc.Configs[1])
	}
	mounts, err := SecretMounts(&svc, st, dir, root, "p-db")
	if err != nil {
		t.Fatal(err)
	}
	byDest := map[string]string{}
	for _, m := range mounts {
		if m.Type != "bind" || strings.Join(m.Options, ",") != "rbind,ro" {
			t.Errorf("mount %+v", m)
		}
		byDest[m.Destination] = m.Source
	}
	for dest, want := range map[string]string{
		"/run/secrets/db_password": "hunter2",
		"/run/secrets/keys/api":    "k-123",
		"/etc/tuned.conf":          "x=1",
	} {
		src := byDest[dest]
		if !strings.HasPrefix(src, filepath.Join(root, "p-db")+string(filepath.Separator)) {
			t.Errorf("%s: copy %q not under the secrets dir", dest, src)
		}
		if b, _ := os.ReadFile(src); string(b) != want {
			t.Errorf("%s: %q", dest, b)
		}
	}
	// A plain config binds its file; it is not copied.
	if byDest["/pg_conf"] != filepath.Join(dir, "postgresql.conf") {
		t.Errorf("config: %q", byDest["/pg_conf"])
	}
	modes := map[string]os.FileMode{"/run/secrets/db_password": 0o400, "/run/secrets/keys/api": 0o440, "/etc/tuned.conf": 0o444}
	for dest, want := range modes {
		if fi, _ := os.Stat(byDest[dest]); fi.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", dest, fi.Mode().Perm(), want)
		}
	}
	// Same content: same path. A changed secret gets a new path (the spec
	// changes, compose up recreates) and the old copy goes.
	again, _ := SecretMounts(&svc, st, dir, root, "p-db")
	if again[0].Source != mounts[0].Source {
		t.Error("unchanged secret moved")
	}
	writeFiles(t, dir, map[string]string{"db_password.txt": "correct horse"})
	changed, err := SecretMounts(&svc, st, dir, root, "p-db")
	if err != nil || changed[0].Source == mounts[0].Source {
		t.Fatalf("changed secret kept its path: %v", err)
	}
	if _, err := os.Stat(mounts[0].Source); !os.IsNotExist(err) {
		t.Error("stale copy kept")
	}
	// A service without secrets leaves nothing behind.
	if m, err := SecretMounts(&Service{}, st, dir, root, "p-db"); err != nil || m != nil {
		t.Fatal(m, err)
	}
	if _, err := os.Stat(filepath.Join(root, "p-db")); !os.IsNotExist(err) {
		t.Error("secrets dir kept for a service without secrets")
	}
	// Errors: no runtime dir, missing file.
	if _, err := SecretMounts(&svc, st, dir, "", "p-db"); err == nil || !strings.Contains(err.Error(), "runtime directory") {
		t.Errorf("no secrets dir: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "api_key.txt"))
	if _, err := SecretMounts(&svc, st, dir, root, "p-db"); err == nil || !strings.Contains(err.Error(), `secret "api_key"`) {
		t.Errorf("missing file: %v", err)
	}
}

func TestSecretsValidationAndWarnings(t *testing.T) {
	if _, err := Parse([]byte("services:\n  a:\n    image: x\n    secrets: [nope]\n")); err == nil || !strings.Contains(err.Error(), `unknown secret "nope"`) {
		t.Errorf("unknown secret: %v", err)
	}
	if _, err := Parse([]byte("services:\n  a:\n    image: x\n    configs: [nope]\n")); err == nil || !strings.Contains(err.Error(), `unknown config "nope"`) {
		t.Errorf("unknown config: %v", err)
	}
	for _, bad := range []string{
		"secrets: [{target: x}]", "secrets: [{source: s, mode: rw}]", "secrets: [{source: s, uid: me}]",
		"secrets: [{source: s, color: red}]", "secrets: [[s]]", "secrets: ['']",
	} {
		if _, err := Parse([]byte("services:\n  a:\n    image: x\n    " + bad + "\nsecrets:\n  s: {file: f}\n")); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	st, err := Parse([]byte("services:\n  a:\n    image: x\n    secrets: [env, ext]\nsecrets:\n  env: {environment: X}\n  ext: {external: true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	ws := SecretsWarnings(st)
	if len(ws) != 2 || !strings.Contains(strings.Join(ws, "\n"), "environment: is not supported") || !strings.Contains(strings.Join(ws, "\n"), "external secret") {
		t.Errorf("warnings: %q", ws)
	}
	svc := st.Services["a"]
	if m, err := SecretMounts(&svc, st, t.TempDir(), t.TempDir(), "a"); err != nil || len(m) != 0 {
		t.Errorf("skipped sources: %v %v", m, err)
	}
	for in, want := range map[string]uint32{"0440": 0o440, "0o600": 0o600, "288": 288} {
		if got, err := parseFileMode(in); err != nil || got != want {
			t.Errorf("mode %q: %o %v", in, got, err)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
