package control

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestHandleImageInspect(t *testing.T) {
	inspect := func(s *Server, ref string) (int, string) {
		rec := httptest.NewRecorder()
		s.handleImageInspect(rec, httptest.NewRequest(http.MethodGet, "/v1/images/inspect?ref="+ref, nil))
		return rec.Code, rec.Body.String()
	}
	if code, _ := inspect(&Server{}, "x"); code != http.StatusServiceUnavailable {
		t.Errorf("no store: %d", code)
	}
	store, err := image.NewStore(filepath.Join(t.TempDir(), "images"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: "amd64"}
	digest, data, err := image.ConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBlob(data); err != nil {
		t.Fatal(err)
	}
	m := &oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
		Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: digest, Size: int64(len(data))}}
	if err := store.SaveImage("app:1", m, cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	if code, _ := inspect(s, "%20"); code != http.StatusBadRequest {
		t.Errorf("no ref: %d", code)
	}
	if code, body := inspect(s, "other:1"); code != http.StatusNotFound || !strings.Contains(body, "other:1") {
		t.Errorf("unknown image: %d %s", code, body)
	}
	if code, body := inspect(s, "app:1"); code != http.StatusOK || !strings.Contains(body, digest) {
		t.Errorf("app:1: %d %s", code, body)
	}
}
