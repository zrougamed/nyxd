package image

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/layer"
	"github.com/zrougamed/nyxd/pkg/oci"
)

const (
	annotationRefName       = "org.opencontainers.image.ref.name"
	annotationContainerdRef = "io.containerd.image.name"
)

// dockerArchiveEntry is one element of a docker-archive manifest.json.
type dockerArchiveEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

// Save writes refs as one tar archive containing an OCI image layout
// (oci-layout, index.json, blobs/sha256/...) plus a docker-archive
// manifest.json, the format `docker save` produces since Docker 25, so the
// result loads with nyx, docker and podman.
func (s *Store) Save(w io.Writer, refs []string) error {
	if len(refs) == 0 {
		return errors.New("save: no images given")
	}
	type item struct {
		ref      string
		manifest []byte
		m        *oci.Manifest
	}
	var items []item
	blobs := map[string]bool{}
	for _, ref := range refs {
		m, _, err := s.LoadImageMeta(ref)
		if err != nil {
			return fmt.Errorf("save %s: %w", ref, err)
		}
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		for _, d := range append([]oci.Descriptor{m.Config}, m.Layers...) {
			if !s.HasBlob(d.Digest) {
				return fmt.Errorf("save %s: blob %s missing from the local store", ref, d.Digest)
			}
			blobs[d.Digest] = true
		}
		items = append(items, item{ref: ref, manifest: data, m: m})
	}

	tw := tar.NewWriter(w)
	epoch := time.Unix(0, 0)
	writeFile := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)),
			Typeflag: tar.TypeReg, ModTime: epoch}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "blobs/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: epoch}); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "blobs/sha256/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: epoch}); err != nil {
		return err
	}
	digests := make([]string, 0, len(blobs))
	for d := range blobs {
		digests = append(digests, d)
	}
	sort.Strings(digests)
	for _, d := range digests {
		if err := s.writeBlobEntry(tw, d, epoch); err != nil {
			return err
		}
	}

	index := oci.Index{SchemaVersion: 2, MediaType: oci.MediaTypeImageIndex}
	var docker []dockerArchiveEntry
	written := map[string]bool{}
	for _, it := range items {
		sum := sha256.Sum256(it.manifest)
		md := "sha256:" + hex.EncodeToString(sum[:])
		if !written[md] {
			if err := writeFile("blobs/sha256/"+hex.EncodeToString(sum[:]), it.manifest); err != nil {
				return err
			}
			written[md] = true
		}
		mt := it.m.MediaType
		if mt == "" {
			mt = oci.MediaTypeImageManifest
		}
		_, _, tag := parseRef(it.ref)
		index.Manifests = append(index.Manifests, oci.Descriptor{
			MediaType: mt, Digest: md, Size: int64(len(it.manifest)),
			Annotations: map[string]string{annotationContainerdRef: it.ref, annotationRefName: tag},
		})
		e := dockerArchiveEntry{Config: blobName(it.m.Config.Digest), RepoTags: []string{it.ref}}
		for _, l := range it.m.Layers {
			e.Layers = append(e.Layers, blobName(l.Digest))
		}
		docker = append(docker, e)
	}
	idx, _ := json.Marshal(index)
	dm, _ := json.Marshal(docker)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", idx},
		{"manifest.json", dm},
	} {
		if err := writeFile(f.name, f.data); err != nil {
			return err
		}
	}
	return tw.Close()
}

func blobName(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

func (s *Store) writeBlobEntry(tw *tar.Writer, digest string, mtime time.Time) error {
	f, err := os.Open(s.BlobPath(digest))
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: blobName(digest), Mode: 0o644, Size: fi.Size(),
		Typeflag: tar.TypeReg, ModTime: mtime}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, fi.Size())
	return err
}

// Load imports images from an archive written by Save, `docker save` or
// `podman save` (OCI layout and/or docker-archive, optionally compressed).
// Every blob's content is verified against its digest. It returns the
// references written.
func (s *Store) Load(r io.Reader) ([]string, error) {
	tmpRoot, err := s.TempDir()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "load-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	// Same confinement rules as image layers: no escapes, no device nodes.
	if _, err := layer.Apply(r, dir, layer.ApplyOptions{Mode: layer.ModeFlatten}); err != nil {
		return nil, fmt.Errorf("load: read archive: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	ar := &archiveReader{s: s, root: root}

	if _, err := root.Lstat("index.json"); err == nil {
		return ar.loadOCI()
	}
	if _, err := root.Lstat("manifest.json"); err == nil {
		return ar.loadDocker()
	}
	return nil, errors.New("load: not an image archive (no index.json or manifest.json)")
}

type archiveReader struct {
	s    *Store
	root *os.Root
}

// readFile reads a regular file inside the archive (symlinks are not
// followed out of it: os.Root refuses escapes).
func (a *archiveReader) readFile(name string, limit int64) ([]byte, error) {
	fi, err := a.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", name)
	}
	f, err := a.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// importBlob copies an archive file into the store, verifying want (when
// set) and returning the digest and size.
func (a *archiveReader) importBlob(name, want string) (string, int64, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	fi, err := a.root.Lstat(name)
	if err != nil {
		return "", 0, err
	}
	if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s: not a regular file", name)
	}
	src, err := a.root.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer src.Close()
	tmpRoot, err := a.s.TempDir()
	if err != nil {
		return "", 0, err
	}
	dst, err := os.CreateTemp(tmpRoot, "blob-")
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, cErr := io.Copy(io.MultiWriter(dst, h), src)
	if err := dst.Close(); cErr == nil {
		cErr = err
	}
	if cErr != nil {
		os.Remove(dst.Name())
		return "", 0, cErr
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		os.Remove(dst.Name())
		return "", 0, fmt.Errorf("%s: digest mismatch: want %s, got %s", name, want, got)
	}
	if err := a.s.AdoptBlob(dst.Name(), got); err != nil {
		os.Remove(dst.Name())
		return "", 0, err
	}
	return got, n, nil
}

func (a *archiveReader) loadOCI() ([]string, error) {
	data, err := a.readFile("index.json", 4<<20)
	if err != nil {
		return nil, err
	}
	var idx oci.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("load: index.json: %w", err)
	}
	var refs []string
	for _, d := range idx.Manifests {
		m, err := a.manifestFor(d)
		if err != nil {
			return nil, err
		}
		cfg, err := a.importImage(m)
		if err != nil {
			return nil, err
		}
		ref := d.Annotations[annotationContainerdRef]
		if ref == "" {
			// Only a tag: without a repository the image cannot be named.
			ref = "nyx-load:" + shortID(m.Config.Digest)
			if t := d.Annotations[annotationRefName]; t != "" && strings.ContainsAny(t, ":/") {
				ref = t
			}
		}
		if err := a.s.SaveImage(ref, m, cfg); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// manifestFor reads the image manifest for d, following a nested index to
// the entry for this host's platform.
func (a *archiveReader) manifestFor(d oci.Descriptor) (*oci.Manifest, error) {
	for depth := 0; depth < 4; depth++ {
		data, err := a.verifiedBlob(d.Digest, 4<<20)
		if err != nil {
			return nil, err
		}
		if d.MediaType == oci.MediaTypeImageIndex || d.MediaType == oci.MediaTypeDockerManifestList {
			var idx oci.Index
			if err := json.Unmarshal(data, &idx); err != nil {
				return nil, err
			}
			wantOS, wantArch := a.s.pullPlatform()
			dg, err := pickIndexDigest(idx.Manifests, wantOS, wantArch)
			if err != nil {
				return nil, err
			}
			for _, m := range idx.Manifests {
				if m.Digest == dg {
					d = m
				}
			}
			continue
		}
		var m oci.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("load: manifest %s: %w", d.Digest, err)
		}
		if m.MediaType == "" {
			m.MediaType = d.MediaType
		}
		return &m, nil
	}
	return nil, errors.New("load: index nesting too deep")
}

func (a *archiveReader) verifiedBlob(digest string, limit int64) ([]byte, error) {
	data, err := a.readFile(blobName(digest), limit)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("load: blob %s: digest mismatch", digest)
	}
	return data, nil
}

func (a *archiveReader) importImage(m *oci.Manifest) (*oci.ImageConfig, error) {
	for _, d := range append([]oci.Descriptor{m.Config}, m.Layers...) {
		if _, _, err := a.importBlob(blobName(d.Digest), d.Digest); err != nil {
			return nil, fmt.Errorf("load: %w", err)
		}
	}
	data, err := os.ReadFile(a.s.BlobPath(m.Config.Digest))
	if err != nil {
		return nil, err
	}
	var cfg oci.ImageConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("load: config: %w", err)
	}
	return &cfg, nil
}

// loadDocker imports a docker-archive (manifest.json with Config/Layers file
// paths; layers are often uncompressed tars named <id>/layer.tar).
func (a *archiveReader) loadDocker() ([]string, error) {
	data, err := a.readFile("manifest.json", 4<<20)
	if err != nil {
		return nil, err
	}
	var entries []dockerArchiveEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("load: manifest.json: %w", err)
	}
	var refs []string
	for _, e := range entries {
		cfgDigest, cfgSize, err := a.importBlob(e.Config, "")
		if err != nil {
			return nil, fmt.Errorf("load: config: %w", err)
		}
		m := &oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
			Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: cfgDigest, Size: cfgSize}}
		for _, l := range e.Layers {
			dg, size, err := a.importBlob(l, "")
			if err != nil {
				return nil, fmt.Errorf("load: layer: %w", err)
			}
			m.Layers = append(m.Layers, oci.Descriptor{MediaType: a.layerMediaType(dg), Digest: dg, Size: size})
		}
		cfgData, err := os.ReadFile(a.s.BlobPath(cfgDigest))
		if err != nil {
			return nil, err
		}
		var cfg oci.ImageConfig
		if err := json.Unmarshal(cfgData, &cfg); err != nil {
			return nil, fmt.Errorf("load: config: %w", err)
		}
		tags := e.RepoTags
		if len(tags) == 0 {
			tags = []string{"nyx-load:" + shortID(cfgDigest)}
		}
		for _, t := range tags {
			if err := a.s.SaveImage(t, m, &cfg); err != nil {
				return nil, err
			}
			refs = append(refs, t)
		}
	}
	return refs, nil
}

func (a *archiveReader) layerMediaType(digest string) string {
	f, err := os.Open(a.s.BlobPath(digest))
	if err != nil {
		return oci.MediaTypeImageLayer
	}
	defer f.Close()
	head, _ := bufio.NewReader(f).Peek(2)
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		return oci.MediaTypeImageLayerGzip
	}
	return oci.MediaTypeImageLayer
}

func shortID(digest string) string {
	h := strings.TrimPrefix(digest, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
