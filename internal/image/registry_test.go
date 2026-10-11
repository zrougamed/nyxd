package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// fakeRegistry is a minimal OCI distribution server with Bearer token auth.
type fakeRegistry struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte // repo:tag -> body
	types     map[string]string
	uploads   int
	user      string
	pass      string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	r := &fakeRegistry{t: t, blobs: map[string][]byte{}, manifests: map[string][]byte{},
		types: map[string]string{}, user: "marwa", pass: "s3cret"}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "http://") }

func (r *fakeRegistry) serve(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/token" {
		u, p, ok := req.BasicAuth()
		scope := req.URL.Query().Get("scope")
		if strings.HasSuffix(scope, "push") && (!ok || u != r.user || p != r.pass) {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"token": "tok:" + scope})
		return
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer tok:") {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake"`, r.srv.URL))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	needPush := req.Method != http.MethodGet && req.Method != http.MethodHead
	if needPush && !strings.HasSuffix(req.Header.Get("Authorization"), "push") {
		http.Error(w, "push scope required", http.StatusForbidden)
		return
	}
	p := strings.TrimPrefix(req.URL.Path, "/v2/")
	if p == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case strings.Contains(p, "/blobs/uploads/") && req.Method == http.MethodPost:
		w.Header().Set("Location", "/v2/"+strings.SplitN(p, "/blobs/", 2)[0]+"/blobs/uploads/u1")
		w.WriteHeader(http.StatusAccepted)
	case strings.Contains(p, "/blobs/uploads/") && req.Method == http.MethodPut:
		data, _ := io.ReadAll(req.Body)
		d := req.URL.Query().Get("digest")
		sum := sha256.Sum256(data)
		if d != "sha256:"+hex.EncodeToString(sum[:]) {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[d] = data
		r.uploads++
		w.WriteHeader(http.StatusCreated)
	case strings.Contains(p, "/blobs/"):
		d := p[strings.LastIndex(p, "/")+1:]
		data, ok := r.blobs[d]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(data)
	case strings.Contains(p, "/manifests/"):
		parts := strings.SplitN(p, "/manifests/", 2)
		key := parts[0] + ":" + parts[1]
		if req.Method == http.MethodPut {
			data, _ := io.ReadAll(req.Body)
			r.manifests[key] = data
			r.types[key] = req.Header.Get("Content-Type")
			w.WriteHeader(http.StatusCreated)
			return
		}
		data, ok := r.manifests[key]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", r.types[key])
		w.Write(data)
	default:
		http.NotFound(w, req)
	}
}

// seedImage stores a one-layer image under ref and returns its config digest.
func seedImage(t *testing.T, s *Store, ref string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.Close()
	gz.Close()
	layerDigest, err := s.PutBlob(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: "amd64", RootFS: oci.RootFS{Type: "layers", DiffIDs: []string{"sha256:x"}}}
	cfgDigest, data, _ := ConfigDigest(cfg)
	if _, err := s.PutBlob(data); err != nil {
		t.Fatal(err)
	}
	m := &oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
		Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: cfgDigest, Size: int64(len(data))},
		Layers: []oci.Descriptor{{MediaType: oci.MediaTypeImageLayerGzip, Digest: layerDigest, Size: int64(buf.Len())}}}
	if err := s.SaveImage(ref, m, cfg); err != nil {
		t.Fatal(err)
	}
	return cfgDigest
}

func TestPushPullRoundTrip(t *testing.T) {
	reg := newFakeRegistry(t)
	src, _ := NewStore(t.TempDir())
	ref := reg.host() + "/team/app:1.0"
	cfgDigest := seedImage(t, src, ref)
	auth := &RegistryAuth{Username: "marwa", Password: "s3cret"}

	var events []PullEvent
	digest, err := src.Push(context.Background(), ref, auth, func(e PullEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") || reg.uploads != 2 || len(reg.manifests) != 1 {
		t.Fatalf("digest=%s uploads=%d manifests=%d", digest, reg.uploads, len(reg.manifests))
	}

	// Second push: everything exists already.
	if _, err := src.Push(context.Background(), ref, auth, nil); err != nil {
		t.Fatal(err)
	}
	if reg.uploads != 2 {
		t.Fatalf("re-push uploaded again: %d", reg.uploads)
	}

	// Wrong credentials are rejected with an auth error.
	if _, err := src.Push(context.Background(), ref, &RegistryAuth{Username: "x", Password: "y"}, nil); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("push with bad credentials: err = %v, want a 401 token error", err)
	}

	// Anonymous pull of the pushed image into a fresh store (loopback => http).
	dst, _ := NewStore(t.TempDir())
	if _, err := dst.Pull(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	m, _, err := dst.LoadImageMeta(ref)
	if err != nil || m.Config.Digest != cfgDigest {
		t.Fatalf("pulled config %v, err %v", m, err)
	}
}

func TestParseChallenge(t *testing.T) {
	scheme, p := parseChallenge(`Bearer realm="https://auth.example/token",service="registry.example",scope="repository:a/b:pull"`)
	if scheme != "Bearer" || p["realm"] != "https://auth.example/token" || p["service"] != "registry.example" || p["scope"] != "repository:a/b:pull" {
		t.Fatalf("%s %v", scheme, p)
	}
	scheme, p = parseChallenge(`Basic realm=registry`)
	if scheme != "Basic" || p["realm"] != "registry" {
		t.Fatalf("%s %v", scheme, p)
	}
}

func TestLoopbackRegistry(t *testing.T) {
	for reg, want := range map[string]bool{
		"localhost:5000": true, "127.0.0.1:5000": true, "[::1]:5000": true, "localhost": true,
		"registry-1.docker.io": false, "ghcr.io": false, "10.0.0.5:5000": false,
	} {
		if got := isLoopbackRegistry(reg); got != want {
			t.Errorf("%s: %v", reg, got)
		}
	}
}
