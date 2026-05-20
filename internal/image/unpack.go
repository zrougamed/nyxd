package image

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ImageConfig is the OCI image configuration (config.json equivalent).
type ImageConfig struct {
	Architecture string     `json:"architecture"`
	OS           string     `json:"os"`
	Config       RunConfig  `json:"config"`
	RootFS       RootFS     `json:"rootfs"`
}

// RunConfig holds the runtime settings from the image.
type RunConfig struct {
	User       string            `json:"User"`
	Env        []string          `json:"Env"`
	Entrypoint []string          `json:"Entrypoint"`
	Cmd        []string          `json:"Cmd"`
	WorkingDir string            `json:"WorkingDir"`
	Labels     map[string]string `json:"Labels"`
}

// RootFS lists the layer diff IDs.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

// Unpacker extracts OCI layers onto disk for overlayfs use.
type Unpacker struct {
	store    *Store
	layerDir string // root directory for extracted layers
}

// NewUnpacker creates an Unpacker backed by the image Store.
func NewUnpacker(store *Store, layerDir string) *Unpacker {
	return &Unpacker{store: store, layerDir: layerDir}
}

// UnpackResult contains paths needed to set up overlayfs.
type UnpackResult struct {
	// Layers are the extracted layer directories in order (bottom → top).
	Layers []string
	// Config is the parsed OCI image config.
	Config *ImageConfig
	// ConfigDigest is the config blob digest.
	ConfigDigest string
}

// Unpack extracts all layers for the manifest at manifestDigest.
// Returns the paths to the extracted layer directories.
func (u *Unpacker) Unpack(manifestDigest string) (*UnpackResult, error) {
	manifest, err := u.store.LoadManifest(manifestDigest)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	// Load image config
	cfg, err := u.loadConfig(manifest.Config.Digest)
	if err != nil {
		return nil, fmt.Errorf("load image config: %w", err)
	}

	result := &UnpackResult{
		Config:       cfg,
		ConfigDigest: manifest.Config.Digest,
		Layers:       make([]string, 0, len(manifest.Layers)),
	}

	for i, layer := range manifest.Layers {
		layerPath, err := u.unpackLayer(layer.Digest, i)
		if err != nil {
			return nil, fmt.Errorf("unpack layer %d (%s): %w", i, layer.Digest, err)
		}
		result.Layers = append(result.Layers, layerPath)
	}

	return result, nil
}

func (u *Unpacker) loadConfig(digest string) (*ImageConfig, error) {
	data, err := os.ReadFile(u.store.BlobPath(digest))
	if err != nil {
		return nil, err
	}
	var cfg ImageConfig
	return &cfg, json.Unmarshal(data, &cfg)
}

// layerExtractPath returns the directory where a layer should be extracted.
func (u *Unpacker) layerExtractPath(digest string) string {
	_, hex, _ := strings.Cut(digest, ":")
	return filepath.Join(u.layerDir, hex)
}

// unpackLayer extracts a single compressed layer tarball to disk.
// Idempotent: skips if the layer directory already exists and has a
// ".done" sentinel file.
func (u *Unpacker) unpackLayer(digest string, index int) (string, error) {
	dest := u.layerExtractPath(digest)
	done := filepath.Join(dest, ".done")

	if _, err := os.Stat(done); err == nil {
		slog.Debug("layer already extracted", "digest", digest, "path", dest)
		return dest, nil
	}

	slog.Info("extracting layer", "index", index, "digest", digest)

	blobPath := u.store.BlobPath(digest)
	f, err := os.Open(blobPath)
	if err != nil {
		return "", fmt.Errorf("open blob: %w", err)
	}
	defer f.Close()

	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}

	if err := extractTarGz(f, dest); err != nil {
		// Clean up partial extraction
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("extract tar: %w", err)
	}

	// Write sentinel
	if err := os.WriteFile(done, []byte("ok"), 0o400); err != nil {
		return "", err
	}

	return dest, nil
}

// extractTarGz decompresses a gzip-compressed tar into dest.
// Security hardened: rejects path traversal, absolute paths, devices, suid bits.
func extractTarGz(r io.Reader, dest string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		// Might be uncompressed (zstd would need separate handling)
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		if err := extractEntry(tr, hdr, dest); err != nil {
			return fmt.Errorf("entry %q: %w", hdr.Name, err)
		}
	}
	return nil
}

// extractEntry writes a single tar entry to dest, applying security checks.
func extractEntry(r io.Reader, hdr *tar.Header, dest string) error {
	// OCI whiteout: .wh. prefix means delete the file in the merged view.
	// For individual layer extraction we just skip them — overlayfs handles
	// the actual whiteout at runtime.
	name := filepath.Clean(hdr.Name)
	if strings.HasPrefix(filepath.Base(name), ".wh.") {
		// Write an overlayfs whiteout char device placeholder
		return extractWhiteout(name, dest)
	}

	// Security: reject path traversal
	targetPath := filepath.Join(dest, name)
	if !strings.HasPrefix(targetPath+string(filepath.Separator), dest+string(filepath.Separator)) {
		return fmt.Errorf("path traversal rejected: %q", hdr.Name)
	}

	// Strip suid/sgid bits
	mode := hdr.FileInfo().Mode() &^ (os.ModeSetuid | os.ModeSetgid)

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(targetPath, mode)

	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, io.LimitReader(r, 1<<30)) // 1 GB per file limit
		syncErr := f.Sync()
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		return closeErr

	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		// Remove existing symlink if present
		_ = os.Remove(targetPath)
		return os.Symlink(hdr.Linkname, targetPath)

	case tar.TypeLink:
		linkTarget := filepath.Join(dest, filepath.Clean(hdr.Linkname))
		if !strings.HasPrefix(linkTarget+string(filepath.Separator), dest+string(filepath.Separator)) {
			return fmt.Errorf("hardlink traversal rejected: %q", hdr.Linkname)
		}
		_ = os.Remove(targetPath)
		return os.Link(linkTarget, targetPath)

	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		// Reject device nodes in layers — OCI spec allows them in some
		// system images but they are a security risk in untrusted images.
		// Allowed to be created only from trusted layers.
		slog.Warn("skipping device node in layer", "path", hdr.Name, "type", hdr.Typeflag)
		return nil

	default:
		slog.Debug("skipping unknown tar entry type", "path", hdr.Name, "type", hdr.Typeflag)
		return nil
	}
}

// extractWhiteout creates an overlayfs-compatible whiteout char device (0,0).
func extractWhiteout(name, dest string) error {
	realName := strings.TrimPrefix(filepath.Base(name), ".wh.")
	targetPath := filepath.Join(dest, filepath.Dir(name), realName)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	// overlayfs whiteout = char device major 0, minor 0
	return syscall.Mknod(targetPath, syscall.S_IFCHR|0o000, 0)
}
