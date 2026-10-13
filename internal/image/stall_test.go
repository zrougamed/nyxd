package image

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// A registry that sends half a blob and then goes silent: the idle timeout
// cuts the stream and the pull resumes from the partial file with Range.
func TestFetchBlobResumesAfterStall(t *testing.T) {
	old := blobIdleTimeout
	blobIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { blobIdleTimeout = old })

	data := []byte(strings.Repeat("nyxd-layer-", 20000))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	half := len(data) / 2

	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/blobs/") {
			return
		}
		if gets.Add(1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Write(data[:half]) //nolint:errcheck
			w.(http.Flusher).Flush()
			<-r.Context().Done() // stall until the client gives up
			return
		}
		var start int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start); err != nil {
			t.Errorf("second GET without Range: %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[start:]) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	s := newStore(t)
	if err := os.MkdirAll(s.root+"/blobs/sha256", 0o700); err != nil {
		t.Fatal(err)
	}
	c := &registryClient{s: newSession(newPullHTTPClient(), strings.TrimPrefix(srv.URL, "http://"), "app", nil, "pull")}
	desc := oci.Descriptor{Digest: digest, Size: int64(len(data))}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.fetchBlobToDisk(ctx, c, desc, nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got, err := os.ReadFile(s.BlobPath(digest))
	if err != nil || string(got) != string(data) {
		t.Fatalf("blob content wrong (err %v, %d bytes)", err, len(got))
	}
	if n := gets.Load(); n != 2 {
		t.Errorf("blob GETs = %d, want 2 (stall then resume)", n)
	}
}

// A registry that never stops sending slowly must not be cut off by a
// whole-request timeout: only silence fails a download.
func TestFetchBlobSlowButSteady(t *testing.T) {
	old := blobIdleTimeout
	blobIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { blobIdleTimeout = old })

	data := []byte(strings.Repeat("x", 4096))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		for i := 0; i < len(data); i += 512 {
			w.Write(data[i : i+512]) //nolint:errcheck
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond) // 800ms total, > idle timeout
		}
	}))
	t.Cleanup(srv.Close)

	s := newStore(t)
	if err := os.MkdirAll(s.root+"/blobs/sha256", 0o700); err != nil {
		t.Fatal(err)
	}
	c := &registryClient{s: newSession(newPullHTTPClient(), strings.TrimPrefix(srv.URL, "http://"), "app", nil, "pull")}
	if err := s.fetchBlobToDisk(context.Background(), c, oci.Descriptor{Digest: digest, Size: int64(len(data))}, nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

// Two pulls of the same layer at once (a retried compose up while the first
// is still running) used to write one .partial file together and could store
// a mix of both under the digest. Now the second waits and reuses the blob.
func TestFetchBlobConcurrentSameDigest(t *testing.T) {
	data := []byte(strings.Repeat("0123456789abcdef", 64*1024)) // 1 MiB
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		for i := 0; i < len(data); i += 64 * 1024 {
			w.Write(data[i : i+64*1024]) //nolint:errcheck
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	s := newStore(t)
	if err := os.MkdirAll(s.root+"/blobs/sha256", 0o700); err != nil {
		t.Fatal(err)
	}
	desc := oci.Descriptor{Digest: digest, Size: int64(len(data))}
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			c := &registryClient{s: newSession(newPullHTTPClient(), strings.TrimPrefix(srv.URL, "http://"), "app", nil, "pull")}
			errs <- s.fetchBlobToDisk(context.Background(), c, desc, nil)
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Errorf("fetch %d: %v", i, err)
		}
	}
	got, _ := os.ReadFile(s.BlobPath(digest))
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("stored blob is corrupt (%d bytes)", len(got))
	}
	if n := gets.Load(); n != 1 {
		t.Errorf("downloaded %d times, want 1", n)
	}
}

// A blob already stored with the wrong size (damaged by the race above) is
// reported by ResolvePulledImage and replaced by the next fetch.
func TestDamagedBlobIsRefetched(t *testing.T) {
	data := []byte(strings.Repeat("layer", 1000))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(data) })) //nolint:errcheck
	t.Cleanup(srv.Close)

	s := newStore(t)
	desc := oci.Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: digest, Size: int64(len(data))}
	if err := os.MkdirAll(s.root+"/blobs/sha256", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.BlobPath(digest), data[:100], 0o600); err != nil {
		t.Fatal(err)
	}
	cfgDigest, err := s.PutBlob([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	m := &oci.Manifest{Config: oci.Descriptor{Digest: cfgDigest, Size: 2}, Layers: []oci.Descriptor{desc}}
	if err := s.writeImageMeta("damaged:1", m, &oci.ImageConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ResolvePulledImage("damaged:1"); err == nil || !strings.Contains(err.Error(), "damaged layer blob") {
		t.Fatalf("resolve damaged: %v", err)
	}
	c := &registryClient{s: newSession(newPullHTTPClient(), strings.TrimPrefix(srv.URL, "http://"), "app", nil, "pull")}
	if err := s.fetchBlobToDisk(context.Background(), c, desc, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ResolvePulledImage("damaged:1"); err != nil {
		t.Errorf("after refetch: %v", err)
	}
}
