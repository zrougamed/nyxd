package image

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func newKey(t *testing.T, dir, name string) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return k, p
}

// sign stores a cosign key signature for digest in repo, the way
// `cosign sign --key` lays it out: a simple-signing payload layer at tag
// sha256-<hex>.sig, its signature in the layer annotation.
func sign(t *testing.T, reg *fakeRegistry, repo, digest, signedDigest string, key *ecdsa.PrivateKey) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"critical": map[string]any{
			"identity": map[string]string{"docker-reference": reg.host() + "/" + repo},
			"image":    map[string]string{"docker-manifest-digest": signedDigest},
			"type":     "cosign container image signature",
		},
		"optional": nil,
	})
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	pd := "sha256:" + hex.EncodeToString(sum[:])
	cfg := []byte("{}")
	cs := sha256.Sum256(cfg)
	cd := "sha256:" + hex.EncodeToString(cs[:])
	m, _ := json.Marshal(oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
		Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: cd, Size: 2},
		Layers: []oci.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: pd, Size: int64(len(payload)),
			Annotations: map[string]string{cosignSignatureAnnotation: base64.StdEncoding.EncodeToString(sig)}}}})
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.blobs[pd], reg.blobs[cd] = payload, cfg
	tag := repo + ":" + strings.Replace(digest, ":", "-", 1) + ".sig"
	reg.manifests[tag], reg.types[tag] = m, oci.MediaTypeImageManifest
}

func writePolicy(t *testing.T, dir, body string) *Policy {
	t.Helper()
	p := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pol, err := LoadPolicy(p)
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

func TestCosignPolicy(t *testing.T) {
	reg := newFakeRegistry(t)
	ref, digest := mirrorFixture(t, reg)
	repo := "team/app"
	dir := t.TempDir()
	good, _ := newKey(t, dir, "team.pub")
	other, _ := newKey(t, dir, "other.pub")
	pol := writePolicy(t, dir, "default: reject\nregistries:\n  "+reg.host()+"/team/*:\n    require: signed\n    keys: [team.pub]\n")
	ctx := context.Background()
	pull := func() (*Store, error) {
		s, _ := NewStore(t.TempDir())
		s.SetPolicy(pol)
		_, err := s.Pull(ctx, ref)
		return s, err
	}

	// No signature: rejected before anything is stored.
	s, err := pull()
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "no signature found") {
		t.Fatalf("unsigned: %v", err)
	}
	if _, _, e := s.LoadImageMeta(ref); e == nil {
		t.Fatal("a rejected image was stored")
	}
	// Signed by a key the policy does not list.
	sign(t, reg, repo, digest, digest, other)
	if _, err := pull(); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "does not match any policy key") {
		t.Fatalf("wrong key: %v", err)
	}
	// Right key, but the payload names another image.
	sign(t, reg, repo, digest, "sha256:"+strings.Repeat("0", 64), good)
	if _, err := pull(); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "signature is for") {
		t.Fatalf("other digest: %v", err)
	}
	// A good signature: pulled, marked verified, allowed to run.
	sign(t, reg, repo, digest, digest, good)
	s, err = pull()
	if err != nil {
		t.Fatalf("signed: %v", err)
	}
	if w, err := s.CheckRunPolicy(ref); err != nil || w != "" {
		t.Fatalf("run after verified pull: %q %v", w, err)
	}
	// The same image brought in another way (nyx load, a tag) is not verified.
	if err := s.Tag(ref, reg.host()+"/team/copy:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckRunPolicy(reg.host() + "/team/copy:1"); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "pull it again") {
		t.Fatalf("unverified copy: %v", err)
	}
	// default: reject for images no entry covers; warn just warns.
	if _, err := s.CheckRunPolicy("docker.io/library/alpine:3.20"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("default reject: %v", err)
	}
	warnPol := writePolicy(t, dir, "default: warn\n")
	s2, _ := NewStore(t.TempDir())
	s2.SetPolicy(warnPol)
	var events []PullEvent
	if _, err := s2.PullWithProgress(ctx, ref, nil, func(e PullEvent) { events = append(events, e) }); err != nil {
		t.Fatalf("warn pull: %v", err)
	}
	found := false
	for _, e := range events {
		found = found || (e.Phase == "policy" && strings.Contains(e.Message, "not verified"))
	}
	if !found {
		t.Errorf("no warning event: %+v", events)
	}
	if w, err := s2.CheckRunPolicy(ref); err != nil || !strings.Contains(w, "not verified") {
		t.Errorf("warn run: %q %v", w, err)
	}
	// No policy: nothing checked.
	s3, _ := NewStore(t.TempDir())
	if w, err := s3.CheckRunPolicy(ref); w != "" || err != nil {
		t.Errorf("no policy: %q %v", w, err)
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	dir := t.TempDir()
	_, keyFile := newKey(t, dir, "k.pub")
	_ = os.WriteFile(filepath.Join(dir, "notpem.pub"), []byte("hello"), 0o644)
	rsaDER := []byte("-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE\n-----END PUBLIC KEY-----\n")
	_ = os.WriteFile(filepath.Join(dir, "broken.pub"), rsaDER, 0o644)
	for name, body := range map[string]string{
		"bad default": "default: maybe\n",
		"bad require": "registries:\n  x/*:\n    require: sometimes\n",
		"no keys":     "registries:\n  x/*:\n    require: signed\n",
		"missing key": "registries:\n  x/*:\n    require: signed\n    keys: [nope.pub]\n",
		"not pem":     "registries:\n  x/*:\n    require: signed\n    keys: [notpem.pub]\n",
		"broken key":  "registries:\n  x/*:\n    require: signed\n    keys: [broken.pub]\n",
		"bad pattern": "registries:\n  'x/[':\n    require: none\n",
		"bad yaml":    "default: [\n",
	} {
		p := filepath.Join(dir, "p.yaml")
		_ = os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadPolicy(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := LoadPolicy(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("missing file")
	}
	// Docker Hub patterns match normalized references; the most specific wins.
	p := filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(p, []byte("registries:\n  docker.io/library/*:\n    require: none\n  docker.io/library/alpine:\n    require: signed\n    keys: ["+keyFile+"]\n"), 0o644)
	pol, err := LoadPolicy(p)
	if err != nil || pol.Default != PolicyAllow {
		t.Fatal(pol, err)
	}
	if r := pol.rule("alpine:3.20"); r == nil || !r.signed {
		t.Errorf("alpine rule: %+v", r)
	}
	if r := pol.rule("busybox"); r == nil || r.signed {
		t.Errorf("library rule: %+v", r)
	}
	if pol.rule("ghcr.io/x/y") != nil || (*Policy)(nil).rule("x") != nil {
		t.Error("unmatched")
	}
}
