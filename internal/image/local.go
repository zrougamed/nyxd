package image

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// TempDir returns a scratch directory on the same filesystem as the blob
// store, so finished blobs can be renamed into place.
func (s *Store) TempDir() (string, error) {
	dir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// AdoptBlob moves the file at src into the blob store under digest. src must
// already hold content with that digest and live in TempDir.
func (s *Store) AdoptBlob(src, digest string) error {
	dst := s.BlobPath(digest)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if s.HasBlob(digest) {
		return os.Remove(src)
	}
	return os.Rename(src, dst)
}

// PutBlob stores data and returns its sha256 digest.
func (s *Store) PutBlob(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if s.HasBlob(digest) {
		return digest, nil
	}
	dst := s.BlobPath(digest)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	return digest, atomicWrite(dst, data)
}

// SaveImage records a locally built image under ref: the config is stored
// as a blob and the manifest + config are written as the ref's metadata,
// the same layout a pull produces.
func (s *Store) SaveImage(ref string, m *oci.Manifest, cfg *oci.ImageConfig) error {
	if _, err := ParseRef(ref); err != nil {
		return fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	for _, l := range m.Layers {
		if !s.HasBlob(l.Digest) {
			return fmt.Errorf("layer blob %s missing", l.Digest)
		}
	}
	return s.writeImageMeta(ref, m, cfg)
}

// Tag makes dst refer to the same manifest and config as src.
func (s *Store) Tag(src, dst string) error {
	m, cfg, err := s.LoadImageMeta(src)
	if err != nil {
		return err
	}
	return s.SaveImage(dst, m, cfg)
}

// ConfigDigest returns the digest of cfg's canonical JSON (the image ID).
func ConfigDigest(cfg *oci.ImageConfig) (string, []byte, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), data, nil
}
