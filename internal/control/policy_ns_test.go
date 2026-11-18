//go:build linux

package control_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// An image the policy requires to be signed, but that came in without a
// verified pull (here: built locally by the test harness), does not run.
func TestControlImagePolicyAtRun(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := start(t)
	dir := t.TempDir()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	_ = os.WriteFile(filepath.Join(dir, "k.pub"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "policy.yaml"), []byte("registries:\n  docker.io/test/*:\n    require: signed\n    keys: [k.pub]\n"), 0o644)
	pol, err := image.LoadPolicy(filepath.Join(dir, "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	d.Store.SetPolicy(pol)
	code, out := d.Do(t, "POST", "/v1/containers/run", map[string]any{"id": "unsigned", "image": "test/app:1", "args": []string{"true"}})
	if code != 403 || !strings.Contains(out, "image policy") || !strings.Contains(out, "pull it again") {
		t.Errorf("run: %d %s", code, out)
	}
	yaml := "services:\n  app:\n    image: test/app:1\n    command: [sleep, '1']\n"
	code, out = d.Do(t, "POST", "/v1/compose/up", map[string]any{"yaml": yaml, "context_dir": dir, "project": "pol"})
	if code != 400 || !strings.Contains(out, "image policy") {
		t.Errorf("compose: %d %s", code, out)
	}
	d.Store.SetPolicy(nil)
	run(t, d, map[string]any{"id": "nopolicy", "args": []string{"true"}, "restart": "no"})
}
