// Package registry is a read-only OCI distribution registry for tests: it
// serves the images of an image.Store over plain HTTP on 127.0.0.1, which
// nyxd talks to without TLS (loopback registry).
package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// Registry serves Store; Host is "127.0.0.1:PORT", so images are pulled as
// Host + "/" + repo + ":" + tag for a ref "repo:tag" saved in Store.
type Registry struct {
	Store *image.Store
	Host  string
	// Requests counts handled requests (to tell cached pulls apart).
	Requests atomic.Int64
	srv      *httptest.Server
}

// New starts a registry for store; Close stops it.
func New(store *image.Store) *Registry {
	r := &Registry{Store: store}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	r.Host = strings.TrimPrefix(r.srv.URL, "http://")
	return r
}

// Close stops the server.
func (r *Registry) Close() { r.srv.Close() }

func (r *Registry) serve(w http.ResponseWriter, req *http.Request) {
	r.Requests.Add(1)
	p := strings.TrimPrefix(req.URL.Path, "/v2/")
	switch {
	case req.URL.Path == "/v2/" || req.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
	case strings.Contains(p, "/manifests/"):
		name, ref, _ := strings.Cut(p, "/manifests/")
		m, _, err := r.Store.LoadImageMeta(name + ":" + ref)
		if err != nil {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		m.MediaType = oci.MediaTypeImageManifest
		b, _ := json.Marshal(m)
		w.Header().Set("Content-Type", oci.MediaTypeImageManifest)
		if req.Method != http.MethodHead {
			_, _ = w.Write(b)
		}
	case strings.Contains(p, "/blobs/"):
		_, digest, _ := strings.Cut(p, "/blobs/")
		f, err := os.Open(r.Store.BlobPath(digest))
		if err != nil {
			http.Error(w, `{"errors":[{"code":"BLOB_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		http.ServeContent(w, req, "", st.ModTime(), f)
	default:
		http.NotFound(w, req)
	}
}
