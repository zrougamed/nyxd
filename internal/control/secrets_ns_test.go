//go:build linux

package control_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// The issue's acceptance test: a secret reaches the container, and its
// value appears in neither nyx inspect nor any state nyxd keeps on disk.
func TestControlComposeSecrets(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	const value = "s3cr3t-hunter2-value"
	if err := os.WriteFile(filepath.Join(dir, "db_password.txt"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.conf"), []byte("mode=prod"), 0o644); err != nil {
		t.Fatal(err)
	}
	yaml := `services:
  db:
    image: test/app:1
    command: [sleep, "300"]
    secrets: [db_password]
    configs: [app_conf]
    environment: {DB_PASSWORD_FILE: /run/secrets/db_password}
secrets:
  db_password: {file: ./db_password.txt}
configs:
  app_conf: {file: ./app.conf}
`
	body := map[string]any{"yaml": yaml, "context_dir": dir, "project": "sec"}
	if code, out := d.Do(t, "POST", "/v1/compose/up", body); code != 200 {
		t.Fatalf("up: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/sec-db/exec", map[string]any{"argv": []string{"cat", "/run/secrets/db_password"}}); code != 200 || !strings.Contains(out, value) {
		t.Fatalf("secret in the container: %d %s", code, out)
	}
	if code, out := d.Do(t, "POST", "/v1/containers/sec-db/exec", map[string]any{"argv": []string{"cat", "/app_conf"}}); code != 200 || !strings.Contains(out, "mode=prod") {
		t.Fatalf("config in the container: %d %s", code, out)
	}
	code, out := d.Do(t, "GET", "/v1/containers/sec-db/inspect", nil)
	if code != 200 || strings.Contains(out, value) || !strings.Contains(out, "/run/secrets/db_password") {
		t.Fatalf("inspect leaks the value or misses the mount: %d %s", code, out)
	}
	// Nothing under the data directory holds the value (supervisor state,
	// bundles, overlays, logs), and the copy sits in the secrets tmpfs dir.
	secretsDir := filepath.Join(d.Base, "secrets")
	_ = filepath.WalkDir(d.Base, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || strings.HasPrefix(p, secretsDir) || strings.Contains(p, string(filepath.Separator)+"merged"+string(filepath.Separator)) {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), value) {
			t.Errorf("secret value found in %s", p)
		}
		return nil
	})
	copies, _ := filepath.Glob(filepath.Join(secretsDir, "sec-db", "db_password-*"))
	if len(copies) != 1 {
		t.Fatalf("copies: %v", copies)
	}
	// Down removes the copies.
	d.Do(t, "POST", "/v1/compose/down", body)
	if _, err := os.Stat(filepath.Join(secretsDir, "sec-db")); !os.IsNotExist(err) {
		t.Errorf("secrets left after down: %v", err)
	}
}
