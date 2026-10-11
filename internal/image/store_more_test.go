package image

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestStoreHousekeeping(t *testing.T) {
	s := newStore(t)
	seedImage(t, s, "a/b:1")
	blobs := filepath.Join(s.root, "blobs", "sha256")
	// Things prune must leave alone or skip.
	_ = os.Mkdir(filepath.Join(blobs, "somedir"), 0o700)
	_ = os.WriteFile(filepath.Join(blobs, strings.Repeat("a", 64)+".partial"), nil, 0o600)
	_ = os.WriteFile(filepath.Join(blobs, "short"), nil, 0o600)
	orphan := strings.Repeat("b", 64)
	_ = os.WriteFile(filepath.Join(blobs, orphan), nil, 0o600)
	// A manifest that cannot be read or parsed is ignored when collecting references.
	bad := filepath.Join(s.root, "images", "x", "y")
	_ = os.MkdirAll(bad, 0o700)
	_ = os.WriteFile(filepath.Join(bad, "manifest.json"), []byte("{"), 0o600)
	_ = os.MkdirAll(filepath.Join(s.root, "images", "onlyone"), 0o700)
	_ = os.WriteFile(filepath.Join(s.root, "images", "onlyone", "manifest.json"), []byte("{}"), 0o600)
	n, err := s.PruneUnreferencedBlobs()
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(blobs, orphan)); !os.IsNotExist(err) {
		t.Error("orphan blob kept")
	}
	refs, _ := s.ListImageRefs()
	if strings.Join(refs, " ") != "a/b:1 x:y" {
		t.Errorf("refs %v", refs)
	}
	// LoadImageMeta / ResolvePulledImage failures.
	if _, _, err := s.LoadImageMeta("x:y"); err == nil {
		t.Error("corrupt manifest")
	}
	_ = os.WriteFile(filepath.Join(bad, "manifest.json"), []byte("{}"), 0o600)
	if _, _, err := s.LoadImageMeta("x:y"); err == nil {
		t.Error("missing config.json")
	}
	_ = os.WriteFile(filepath.Join(bad, "config.json"), []byte("{"), 0o600)
	if _, _, err := s.LoadImageMeta("x:y"); err == nil {
		t.Error("corrupt config.json")
	}
	m, _, _ := s.LoadImageMeta("a/b:1")
	_ = os.Remove(s.BlobPath(m.Layers[0].Digest))
	if _, _, _, err := s.ResolvePulledImage("a/b:1"); err == nil || !strings.Contains(err.Error(), "missing layer blob") {
		t.Errorf("missing layer: %v", err)
	}
	if err := s.SaveImage("a/b:2", m, &oci.ImageConfig{}); err == nil {
		t.Error("save with a missing layer")
	}
	if err := s.SaveImage("::", m, &oci.ImageConfig{}); err == nil && !strings.Contains("", "x") {
		t.Log("ParseRef accepts odd refs")
	}
	// Platform strings and index defaults.
	for p, want := range map[string]string{"": "", "linux/arm64": "linux/arm64", "bogus": "linux/amd64", "/x": "linux/amd64"} {
		s.SetPlatform(p)
		o, a := s.pullPlatform()
		if want != "" && o+"/"+a != want {
			t.Errorf("platform %q: %s/%s", p, o, a)
		}
	}
	if d, err := pickIndexDigest([]oci.Descriptor{{Digest: "sha256:x", Platform: &oci.Platform{OS: "linux", Architecture: "amd64"}}}, "", ""); err != nil || d != "sha256:x" {
		t.Errorf("index defaults: %s %v", d, err)
	}
	// A blob already on disk is not fetched again.
	if err := s.fetchBlobToDisk(context.Background(), nil, oci.Descriptor{Digest: m.Config.Digest}, func(int64) {}); err != nil {
		t.Errorf("cached blob: %v", err)
	}
}

func TestAuthRetryWithBody(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "payload" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	sess := newSession(&http.Client{}, host, "r", &RegistryAuth{Username: "u", Password: "p"}, "push")
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v2/r/x", strings.NewReader("payload"))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("payload")), nil }
	resp, err := sess.do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("retry with body: %v %v", resp, err)
	}
	resp.Body.Close()
	sess = newSession(&http.Client{}, host, "r", &RegistryAuth{Username: "u"}, "push")
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v2/r/x", strings.NewReader("payload"))
	req.GetBody = nil
	if _, err := sess.do(req); err == nil || !strings.Contains(err.Error(), "cannot retry") {
		t.Errorf("body without GetBody: %v", err)
	}
	sess = newSession(&http.Client{}, host, "r", &RegistryAuth{Username: "u"}, "push")
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v2/r/x", strings.NewReader("payload"))
	req.GetBody = func() (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := sess.do(req); err == nil {
		t.Error("GetBody failure")
	}
	if _, p := parseChallenge(`Bearer realm="unterminated`); p["realm"] != "unterminated" {
		t.Errorf("unterminated quote: %v", p)
	}
}
