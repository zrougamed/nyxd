// Package image implements a minimal OCI image puller.
// No registry SDK, no Docker client - raw HTTP + OCI distribution spec.
package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zrougamed/nyxd/pkg/oci"
)

const (
	defaultRegistry    = "registry-1.docker.io"
	maxLayerConcurrent = 3
	pullTimeout        = 60 * time.Minute
	layerBufSize       = 32 * 1024
	blobAttempts       = 5

	// Per-phase limits for registry HTTP. There is deliberately no
	// http.Client.Timeout: it also bounds reading the body, so any layer that
	// takes longer than it to download would be cut off. A stalled body is
	// caught by blobIdleTimeout instead.
	dialTimeout   = 30 * time.Second
	tlsTimeout    = 15 * time.Second
	headerTimeout = 30 * time.Second
)

// blobIdleTimeout fails a blob download that stops sending (a var for tests).
var blobIdleTimeout = 60 * time.Second

// newPullHTTPClient returns the registry client used for pulls.
func newPullHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = tlsTimeout
	tr.ResponseHeaderTimeout = headerTimeout
	return &http.Client{Transport: tr}
}

// ParsedRef holds normalized components of an image reference.
type ParsedRef struct {
	Registry string
	Repo     string
	Tag      string
	Digest   string
}

// ParseRef parses a container image reference (used by tests and callers).
func ParseRef(input string) (ParsedRef, error) {
	reg, repo, tag := parseRef(input)
	pr := ParsedRef{Registry: reg, Repo: repo}
	if strings.Contains(input, "@") {
		pr.Digest = tag
	} else {
		pr.Tag = tag
	}
	return pr, nil
}

// RegistryAuth carries optional HTTP Basic credentials for registry pulls.
type RegistryAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Store manages OCI blobs and image metadata on disk.
//
//	<storeRoot>/blobs/sha256/<hex>          – raw compressed blobs
//	<storeRoot>/images/<repo>/<tag>/        – manifest.json + config.json
type Store struct {
	root     string
	mu       sync.RWMutex
	platform string // e.g. "linux/arm64"; empty => runtime.GOOS/GOARCH when pulling indexes

	// blobLocks serialises downloads of one blob: two pulls sharing a layer
	// (a retried compose up while the first still runs) must not write the
	// same .partial file at once.
	blobLocksMu sync.Mutex
	blobLocks   map[string]*sync.Mutex
}

// lockBlob takes the download lock for digest and returns its unlock.
func (s *Store) lockBlob(digest string) func() {
	s.blobLocksMu.Lock()
	if s.blobLocks == nil {
		s.blobLocks = map[string]*sync.Mutex{}
	}
	l, ok := s.blobLocks[digest]
	if !ok {
		l = &sync.Mutex{}
		s.blobLocks[digest] = l
	}
	s.blobLocksMu.Unlock()
	l.Lock()
	return l.Unlock
}

// NewStore creates (or opens) a blob store at root.
func NewStore(root string) (*Store, error) {
	for _, sub := range []string{"blobs/sha256", "images"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return nil, fmt.Errorf("image store init: %w", err)
		}
	}
	return &Store{root: root}, nil
}

// SetPlatform sets the OS/architecture used when resolving multi-platform indexes (e.g. "linux/arm64").
// Pass empty to use the daemon's runtime.GOOS and runtime.GOARCH.
func (s *Store) SetPlatform(osArch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.platform = strings.TrimSpace(osArch)
}

func (s *Store) pullPlatform() (osName, archName string) {
	s.mu.RLock()
	p := strings.TrimSpace(s.platform)
	s.mu.RUnlock()
	if p == "" {
		p = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}
	a, b, ok := strings.Cut(p, "/")
	if !ok || a == "" || b == "" {
		return "linux", "amd64"
	}
	return a, b
}

// Pull fetches an image from a registry and caches blobs locally.
// ref format: [registry/]name[:tag|@digest]
func (s *Store) Pull(ctx context.Context, ref string) (*oci.ImageConfig, error) {
	return s.PullWithProgress(ctx, ref, nil, nil)
}

// PullWithProgress runs Pull and invokes on for each PullEvent (e.g. NDJSON streaming).
// on must be non-blocking or very fast; the caller serializes if needed.
// auth may be nil for anonymous pulls.
func (s *Store) PullWithProgress(ctx context.Context, ref string, auth *RegistryAuth, on func(PullEvent)) (*oci.ImageConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	var emitMu sync.Mutex
	emit := func(ev PullEvent) {
		if on == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		on(ev)
	}

	emit(PullEvent{Phase: "begin", Ref: ref})

	reg, repo, tag := parseRef(ref)
	client := &registryClient{
		s: newSession(newPullHTTPClient(), reg, repo, auth, "pull"),
	}
	client.wantOS, client.wantArch = s.pullPlatform()

	if err := client.auth(ctx); err != nil {
		return nil, fmt.Errorf("pull auth %s: %w", ref, err)
	}

	emit(PullEvent{Phase: "auth", Ref: ref, Message: "ok"})

	manifest, err := client.manifest(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("pull manifest %s: %w", ref, err)
	}

	emit(PullEvent{
		Phase:      "manifest",
		Ref:        ref,
		MediaType:  manifest.MediaType,
		LayerCount: len(manifest.Layers),
	})

	emit(PullEvent{
		Phase:  "config",
		Digest: manifest.Config.Digest,
		Size:   manifest.Config.Size,
	})

	cfgBlob, err := s.fetchBlob(ctx, client, manifest.Config, func(cur int64) {
		emit(PullEvent{Phase: "progress", Digest: manifest.Config.Digest, Current: cur})
	})
	if err != nil {
		return nil, fmt.Errorf("pull config: %w", err)
	}
	var imgCfg oci.ImageConfig
	if err := json.Unmarshal(cfgBlob, &imgCfg); err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}

	emit(PullEvent{Phase: "layer_done", Digest: manifest.Config.Digest})

	if err := s.pullLayers(ctx, client, manifest.Layers, emit); err != nil {
		return nil, fmt.Errorf("pull layers: %w", err)
	}

	emit(PullEvent{Phase: "meta", Ref: ref, Message: "writing image metadata"})

	if err := s.writeImageMeta(ref, manifest, &imgCfg); err != nil {
		return nil, fmt.Errorf("write image meta: %w", err)
	}

	return &imgCfg, nil
}

// BlobPath returns the on-disk path of a blob by digest.
func (s *Store) BlobPath(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	return filepath.Join(s.root, "blobs", "sha256", hex)
}

// LoadManifest reads an OCI image manifest JSON from the blob store.
func (s *Store) LoadManifest(digest string) (*oci.Manifest, error) {
	data, err := os.ReadFile(s.BlobPath(digest))
	if err != nil {
		return nil, fmt.Errorf("read manifest blob: %w", err)
	}
	var m oci.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &m, nil
}

// HasBlob returns true if the blob is already cached locally.
func (s *Store) HasBlob(digest string) bool {
	_, err := os.Stat(s.BlobPath(digest))
	return err == nil
}

// LoadImageMeta loads a previously pulled image's manifest + config.
func (s *Store) LoadImageMeta(ref string) (*oci.Manifest, *oci.ImageConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadImageMetaUnlocked(ref)
}

func (s *Store) loadImageMetaUnlocked(ref string) (*oci.Manifest, *oci.ImageConfig, error) {
	dir := s.imageDir(ref)

	mData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("image not found locally (pull first): %w", err)
	}
	var m oci.Manifest
	if err := json.Unmarshal(mData, &m); err != nil {
		return nil, nil, err
	}

	cData, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, nil, err
	}
	var cfg oci.ImageConfig
	if err := json.Unmarshal(cData, &cfg); err != nil {
		return nil, nil, err
	}
	return &m, &cfg, nil
}

// ResolvePulledImage returns manifest, config, and on-disk blob paths (base layer first)
// for an image already present in the store (pull first).
func (s *Store) ResolvePulledImage(ref string) (*oci.Manifest, *oci.ImageConfig, []string, error) {
	m, cfg, err := s.LoadImageMeta(ref)
	if err != nil {
		return nil, nil, nil, err
	}
	paths := make([]string, 0, len(m.Layers))
	for _, d := range m.Layers {
		p := s.BlobPath(d.Digest)
		st, err := os.Stat(p)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("missing layer blob %s: %w", d.Digest, err)
		}
		if d.Size > 0 && st.Size() != d.Size {
			// Pulling again replaces it (fetchBlobToDisk checks the size too).
			return nil, nil, nil, fmt.Errorf("damaged layer blob %s: %d bytes, manifest says %d", d.Digest, st.Size(), d.Size)
		}
		paths = append(paths, p)
	}
	return m, cfg, paths, nil
}

// refFile records, next to an image's manifest, the reference it was stored
// under: directory names are sanitised and drop the registry, so they cannot
// be turned back into the reference.
const refFile = "ref"

// imageDir is the metadata directory for ref.
func (s *Store) imageDir(ref string) string {
	_, repo, tag := parseRef(ref)
	return filepath.Join(s.root, "images", sanitisePath(repo), sanitisePath(tag))
}

type storedImage struct{ ref, dir string }

// storedImages lists image metadata directories with their references
// (repo:tag from the directory for images stored before refFile existed).
func (s *Store) storedImages() ([]storedImage, error) {
	imagesRoot := filepath.Join(s.root, "images")
	var out []storedImage
	err := filepath.WalkDir(imagesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "manifest.json" {
			return nil
		}
		dir := filepath.Dir(path)
		if b, err := os.ReadFile(filepath.Join(dir, refFile)); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			out = append(out, storedImage{ref: strings.TrimSpace(string(b)), dir: dir})
			return nil
		}
		rel, err := filepath.Rel(imagesRoot, dir)
		if err != nil {
			return nil
		}
		segs := strings.Split(rel, string(filepath.Separator))
		if len(segs) < 2 {
			return nil
		}
		out = append(out, storedImage{ref: strings.Join(segs[:len(segs)-1], "/") + ":" + segs[len(segs)-1], dir: dir})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ref < out[j].ref })
	return out, err
}

// ListImageRefs returns the references of the images in the store.
func (s *Store) ListImageRefs() ([]string, error) {
	imgs, err := s.storedImages()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(imgs))
	for i, im := range imgs {
		out[i] = im.ref
	}
	return out, nil
}

// RemoveImage deletes local metadata for ref (manifest + config) and prunes layer blobs
// that are no longer referenced by any remaining image metadata.
func (s *Store) RemoveImage(ref string) error {
	s.mu.Lock()
	_, _, err := s.loadImageMetaUnlocked(ref)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	err = os.RemoveAll(s.imageDir(ref))
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("remove image dir: %w", err)
	}
	if _, err := s.PruneUnreferencedBlobs(); err != nil {
		return fmt.Errorf("prune blobs after remove: %w", err)
	}
	return nil
}

// PruneUnreferencedBlobs deletes blobs under blobs/sha256 that are not referenced by any
// manifest.json under images/. Returns the number of files removed.
func (s *Store) PruneUnreferencedBlobs() (int, error) {
	refDigests, err := s.collectReferencedDigests()
	if err != nil {
		return 0, err
	}
	blobDir := filepath.Join(s.root, "blobs", "sha256")
	ents, err := os.ReadDir(blobDir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".partial") {
			continue
		}
		if len(name) != 64 {
			continue
		}
		d := "sha256:" + name
		if refDigests[d] {
			continue
		}
		if err := os.Remove(filepath.Join(blobDir, name)); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove blob %s: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

func (s *Store) collectReferencedDigests() (map[string]bool, error) {
	out := make(map[string]bool)
	imagesRoot := filepath.Join(s.root, "images")
	_ = filepath.WalkDir(imagesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "manifest.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var m oci.Manifest
		if json.Unmarshal(data, &m) != nil {
			return nil
		}
		out[m.Config.Digest] = true
		for _, layer := range m.Layers {
			out[layer.Digest] = true
		}
		return nil
	})
	return out, nil
}

// PruneImagesNotIn removes local image metadata for refs not in keepRefs.
// If dryRun is true, returns refs that would be removed without deleting.
// Images are matched by storage location, so a kept "alpine:3.20" also keeps
// the image listed as "docker.io/library/alpine:3.20".
func (s *Store) PruneImagesNotIn(keepRefs map[string]struct{}, dryRun bool) ([]string, error) {
	imgs, err := s.storedImages()
	if err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(keepRefs))
	for ref := range keepRefs {
		keep[s.imageDir(ref)] = true
	}
	var removed []string
	for _, im := range imgs {
		if keep[im.dir] {
			continue
		}
		if !dryRun {
			if err := os.RemoveAll(im.dir); err != nil {
				return removed, fmt.Errorf("prune %q: %w", im.ref, err)
			}
		}
		removed = append(removed, im.ref)
	}
	if !dryRun && len(removed) > 0 {
		if _, err := s.PruneUnreferencedBlobs(); err != nil {
			return removed, fmt.Errorf("prune blobs: %w", err)
		}
	}
	return removed, nil
}

// pullLayers fetches all layers with bounded concurrency.
func (s *Store) pullLayers(ctx context.Context, client *registryClient, layers []oci.Descriptor, emit func(PullEvent)) error {
	type result struct{ err error }
	results := make(chan result, len(layers))
	sem := make(chan struct{}, maxLayerConcurrent)

	var wg sync.WaitGroup
	for i, layer := range layers {
		cached := s.HasBlob(layer.Digest)
		emit(PullEvent{
			Phase:  "layer",
			Index:  i,
			Count:  len(layers),
			Digest: layer.Digest,
			Size:   layer.Size,
			Cached: cached,
		})
		if cached {
			emit(PullEvent{Phase: "layer_done", Digest: layer.Digest})
			continue
		}
		wg.Add(1)
		go func(desc oci.Descriptor) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			err := s.fetchBlobToDisk(ctx, client, desc, func(cur int64) {
				emit(PullEvent{Phase: "progress", Digest: desc.Digest, Current: cur})
			})
			if err == nil {
				emit(PullEvent{Phase: "layer_done", Digest: desc.Digest})
			}
			results <- result{err}
		}(layer)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var errs []error
	for r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return errors.Join(errs...)
}

// progressReader wraps an io.Reader to emit absolute byte counts periodically.
type progressReader struct {
	r    io.Reader
	step int64
	next int64
	n    int64
	fn   func(int64)
}

func newProgressReader(r io.Reader, fn func(int64)) io.Reader {
	if fn == nil {
		return r
	}
	const step = 256 * 1024
	return &progressReader{r: r, step: step, next: step, fn: fn}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		if p.fn != nil && p.n >= p.next {
			p.fn(p.n)
			for p.next <= p.n {
				p.next += p.step
			}
		}
	}
	if err == io.EOF && p.fn != nil {
		p.fn(p.n)
	}
	return n, err
}

// fetchBlob downloads a small blob (e.g. config) and returns its bytes.
func (s *Store) fetchBlob(ctx context.Context, client *registryClient, desc oci.Descriptor, onProgress func(int64)) ([]byte, error) {
	dest := s.BlobPath(desc.Digest)

	if data, err := os.ReadFile(dest); err == nil {
		if onProgress != nil {
			onProgress(int64(len(data)))
		}
		return data, nil
	}

	if err := s.fetchBlobToDisk(ctx, client, desc, onProgress); err != nil {
		return nil, err
	}
	return os.ReadFile(dest)
}

// fetchBlobToDisk streams a blob to the content store without holding the full payload in memory.
// Interrupted downloads leave "<dest>.partial"; a subsequent pull resumes with HTTP Range when supported.
func (s *Store) fetchBlobToDisk(ctx context.Context, client *registryClient, desc oci.Descriptor, onProgress func(int64)) error {
	dest := s.BlobPath(desc.Digest)
	defer s.lockBlob(desc.Digest)()
	if st, err := os.Stat(dest); err == nil {
		if desc.Size <= 0 || st.Size() == desc.Size {
			if onProgress != nil {
				onProgress(st.Size())
			}
			return nil
		}
		_ = os.Remove(dest) // damaged (wrong size): fetch it again
	}

	partial := dest + ".partial"
	forceFull := false
	for attempt := 0; attempt < blobAttempts; attempt++ {
		err := s.fetchBlobToDiskOnce(ctx, client, desc, dest, partial, onProgress, forceFull)
		if err == nil {
			return nil
		}
		if errors.Is(err, errBlobRangeFallback) {
			_ = os.Remove(partial)
			forceFull = true
			continue
		}
		// A dropped or stalled stream keeps what it wrote: resume from it.
		if errors.Is(err, errBlobStream) && ctx.Err() == nil {
			forceFull = false
			continue
		}
		return err
	}
	return fmt.Errorf("fetch blob %s: too many retries", desc.Digest)
}

var (
	errBlobRangeFallback = errors.New("blob: restart without range")
	errBlobStream        = errors.New("blob stream interrupted")
)

func (s *Store) fetchBlobToDiskOnce(ctx context.Context, client *registryClient, desc oci.Descriptor, dest, partial string, onProgress func(int64), forceFull bool) error {
	var start int64
	h := sha256.New()
	if !forceFull {
		if st, err := os.Stat(partial); err == nil && st.Size() > 0 {
			if st.Size() > desc.Size {
				_ = os.Remove(partial)
			} else {
				f, err := os.Open(partial)
				if err == nil {
					n, err := io.Copy(h, f)
					f.Close()
					if err == nil && n == st.Size() {
						start = st.Size()
					} else {
						_ = os.Remove(partial)
						h = sha256.New()
					}
				}
			}
		}
	} else {
		_ = os.Remove(partial)
	}

	if start == desc.Size {
		got := fmt.Sprintf("sha256:%x", h.Sum(nil))
		if got != desc.Digest {
			_ = os.Remove(partial)
			return fmt.Errorf("digest mismatch on partial: want %s got %s", desc.Digest, got)
		}
		if onProgress != nil {
			onProgress(desc.Size)
		}
		return os.Rename(partial, dest)
	}

	rc, err := client.blob(ctx, desc.Digest, start)
	if err != nil {
		return fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer rc.Close()

	flags := os.O_WRONLY | os.O_CREATE
	if start == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	out, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return err
	}

	base := start
	wrapOn := func(n int64) {
		if onProgress != nil {
			onProgress(base + n)
		}
	}
	pr := newProgressReader(rc, wrapOn)
	tee := io.TeeReader(pr, h)
	buf := make([]byte, layerBufSize)
	if _, err := io.CopyBuffer(out, tee, buf); err != nil {
		_ = out.Close()
		return fmt.Errorf("stream blob: %w: %w", errBlobStream, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	got := fmt.Sprintf("sha256:%x", h.Sum(nil))
	if got != desc.Digest {
		_ = os.Remove(partial)
		return fmt.Errorf("digest mismatch: want %s got %s", desc.Digest, got)
	}
	// The hash covers the bytes read, not the file: make sure nothing else
	// changed the file under us before it becomes the blob.
	if st, err := os.Stat(partial); err != nil || (desc.Size > 0 && st.Size() != desc.Size) {
		_ = os.Remove(partial)
		return errBlobRangeFallback
	}
	if err := os.Rename(partial, dest); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return nil
}

func (s *Store) writeImageMeta(ref string, m *oci.Manifest, cfg *oci.ImageConfig) error {
	// Serialize meta updates for one store: concurrent SaveImage/Tag of the
	// same ref (compose --build overlapping a pull, or two builds) must not
	// interleave writes into the same image directory.
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.imageDir(ref)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, refFile), []byte(ref+"\n")); err != nil {
		return err
	}
	mData, _ := json.MarshalIndent(m, "", "  ")
	if err := atomicWrite(filepath.Join(dir, "manifest.json"), mData); err != nil {
		return err
	}
	cData, _ := json.MarshalIndent(cfg, "", "  ")
	return atomicWrite(filepath.Join(dir, "config.json"), cData)
}

// ─── Registry HTTP client ─────────────────────────────────────────────────────

type registryClient struct {
	s        *session
	wantOS   string
	wantArch string
}

// auth checks the registry API is reachable and obtains credentials up front
// (GET /v2/ answers with the auth challenge), so failures surface as auth
// errors rather than on the first manifest request.
func (c *registryClient) auth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.s.baseURL()+"/v2/", nil)
	if err != nil {
		return err
	}
	resp, err := c.s.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// 401 here is fine for registries that only challenge per repository.
	if resp.StatusCode >= 500 {
		return fmt.Errorf("auth: %s", resp.Status)
	}
	return nil
}

func (c *registryClient) manifest(ctx context.Context, ref string) (*oci.Manifest, error) {
	url := c.s.repoURL("manifests/" + ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", strings.Join([]string{
		oci.MediaTypeImageManifest,
		oci.MediaTypeImageIndex,
		oci.MediaTypeDockerManifestV2,
		oci.MediaTypeDockerManifestList,
	}, ", "))
	resp, err := c.s.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest %s: %s", ref, resp.Status)
	}

	ct := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if strings.Contains(ct, "index") || strings.Contains(ct, "manifest.list") {
		var idx oci.Index
		if err := json.Unmarshal(body, &idx); err != nil {
			return nil, manifestJSONDecodeError(ref, ct, body, err)
		}
		d, err := pickIndexDigest(idx.Manifests, c.wantOS, c.wantArch)
		if err != nil {
			return nil, err
		}
		return c.manifest(ctx, d)
	}

	var m oci.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, manifestJSONDecodeError(ref, ct, body, err)
	}
	return &m, nil
}

func manifestJSONDecodeError(ref, contentType string, body []byte, err error) error {
	b := bytes.TrimSpace(body)
	if len(b) > 0 && b[0] == '<' {
		return fmt.Errorf("registry manifest %s: body was HTML/XML (proxy, captive portal, TLS inspection, or wrong host), not JSON: %w", ref, err)
	}
	if len(b) == 0 {
		return fmt.Errorf("registry manifest %s: empty body (%w)", ref, err)
	}
	return fmt.Errorf("registry manifest %s (content-type %q): %w", ref, contentType, err)
}

func pickIndexDigest(manifests []oci.Descriptor, wantOS, wantArch string) (string, error) {
	if wantOS == "" {
		wantOS = "linux"
	}
	if wantArch == "" {
		wantArch = "amd64"
	}
	for _, m := range manifests {
		if m.Platform == nil || m.Digest == "" {
			continue
		}
		if m.Platform.OS == wantOS && m.Platform.Architecture == wantArch {
			return m.Digest, nil
		}
	}
	return "", fmt.Errorf("no manifest in index for platform %s/%s", wantOS, wantArch)
}

func (c *registryClient) blob(ctx context.Context, digest string, start int64) (io.ReadCloser, error) {
	url := c.s.repoURL("blobs/" + digest)
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	if start > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}

	resp, err := c.s.do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if start > 0 {
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			cancel()
			return nil, errBlobRangeFallback
		}
		if resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			cancel()
			return nil, fmt.Errorf("blob %s: %s", digest, resp.Status)
		}
	} else if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("blob %s: %s", digest, resp.Status)
	}
	return newIdleReader(resp.Body, blobIdleTimeout, cancel), nil
}

// idleReader cancels the request when no bytes arrive for idle, so a
// connection that stays open but stops sending fails instead of hanging.
type idleReader struct {
	rc      io.ReadCloser
	idle    time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled atomic.Bool
}

func newIdleReader(rc io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleReader {
	r := &idleReader{rc: rc, idle: idle, cancel: cancel}
	r.timer = time.AfterFunc(idle, func() {
		r.stalled.Store(true)
		cancel()
	})
	return r
}

func (r *idleReader) Read(b []byte) (int, error) {
	n, err := r.rc.Read(b)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	if err != nil && err != io.EOF && r.stalled.Load() {
		err = fmt.Errorf("no data for %s", r.idle)
	}
	return n, err
}

func (r *idleReader) Close() error {
	r.timer.Stop()
	r.cancel()
	return r.rc.Close()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func parseRef(ref string) (registry, repo, tag string) {
	registry = defaultRegistry
	tag = "latest"

	if idx := strings.Index(ref, "@"); idx != -1 {
		tag = ref[idx+1:]
		ref = ref[:idx]
	} else if idx := strings.LastIndex(ref, ":"); idx != -1 && !strings.Contains(ref[idx:], "/") {
		tag = ref[idx+1:]
		ref = ref[:idx]
	}

	parts := strings.SplitN(ref, "/", 2)
	switch {
	case len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost"):
		registry = parts[0]
		repo = parts[1]
	case len(parts) == 1:
		repo = "library/" + parts[0]
	default:
		repo = ref
	}
	registry = normalizeDockerHubRegistry(registry)
	return registry, repo, tag
}

// normalizeDockerHubRegistry maps Docker Hub front hostnames to the registry API host.
// Requests to https://docker.io/v2/... often return HTML; the OCI API lives at registry-1.docker.io.
func normalizeDockerHubRegistry(host string) string {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "docker.io", "www.docker.io", "registry.docker.io", "hub.docker.com", "registry.hub.docker.com":
		return defaultRegistry
	default:
		return host
	}
}

func sanitisePath(s string) string {
	return strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(s)
}

// atomicWrite writes data to path via a uniquely named temp file in the same
// directory, then renames it into place. A fixed path+".tmp" name is unsafe:
// two concurrent writers (e.g. compose rebuild + pull of the same tag) race on
// the same temp file and rename returns ENOENT ("no such file or directory").
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".nyxd-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
