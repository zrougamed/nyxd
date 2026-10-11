//go:build linux

// Package overlay manages overlayfs mounts for container rootfs.
// Uses kernel overlayfs directly via syscall - no external tools needed.
package overlay

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/zrougamed/nyxd/internal/layer"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// Manager handles overlayfs creation and teardown for containers.
//
// Layout per container:
//
//	<base>/<containerID>/
//	  layers/  – extracted read-only layer dirs (bottom → top)
//	  diff/    – container writable layer (upperdir)
//	  work/    – overlayfs workdir (kernel requirement)
//	  merged/  – merged mount point (container rootfs)
//
// Extracted image layers are shared across containers under
// <base>/_layers/<sha256-hex>/ (a directory only appears there once its
// extraction completed, via atomic rename).
type Manager struct {
	base string // e.g. /var/lib/nyxd/overlay
	opts Options

	dlMu        sync.Mutex
	digestLocks map[string]*sync.Mutex
}

// Options configure a Manager.
type Options struct {
	// Rootless mounts overlayfs with "userxattr" (overlay metadata in user.*
	// xattrs), which is required inside an unprivileged user namespace.
	Rootless bool
	// SingleID is set when the user namespace maps only one ID: every file in
	// an image is then owned by 0, since no other ID exists to chown to.
	SingleID bool
}

// NewManager creates an overlay manager rooted at base.
func NewManager(base string, opts Options) (*Manager, error) {
	if err := os.MkdirAll(filepath.Join(base, layersDirName), 0o700); err != nil {
		return nil, fmt.Errorf("overlay manager init: %w", err)
	}
	removeLegacyCache(base)
	return &Manager{base: base, opts: opts, digestLocks: make(map[string]*sync.Mutex)}, nil
}

const (
	layersDirName = "_layers"
	// legacyCacheDir held layers extracted with host tar (whiteouts and
	// ownership were not applied correctly); it is replaced by _layers.
	legacyCacheDir = "_cache"
)

// removeLegacyCache deletes the pre-_layers cache unless a live overlay mount
// still uses it as a lowerdir (containers adopted across a daemon upgrade).
func removeLegacyCache(base string) {
	legacy := filepath.Join(base, legacyCacheDir)
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	if mounts, err := os.ReadFile("/proc/mounts"); err == nil && strings.Contains(string(mounts), legacy+"/") {
		slog.Info("overlay: legacy layer cache still mounted; keeping it", "path", legacy)
		return
	}
	if err := os.RemoveAll(legacy); err != nil {
		slog.Warn("overlay: remove legacy layer cache", "path", legacy, "err", err)
	}
}

// Prepare unpacks image layers and mounts an overlayfs for containerID.
// blobPaths is ordered lower→upper (first layer is bottom of stack).
// Returns the merged directory path (container rootfs).
func (m *Manager) Prepare(containerID string, blobPaths []string, cfg *oci.ImageConfig) (string, error) {
	cDir := filepath.Join(m.base, containerID)
	layersDir := filepath.Join(cDir, "layers")
	diffDir := filepath.Join(cDir, "diff")
	workDir := filepath.Join(cDir, "work")
	mergedDir := filepath.Join(cDir, "merged")

	for _, d := range []string{layersDir, diffDir, workDir, mergedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", fmt.Errorf("overlay dirs: %w", err)
		}
	}

	// Extract each layer tarball into its own directory (shared content-addressed cache).
	lowerDirs := make([]string, 0, len(blobPaths))
	for _, blobPath := range blobPaths {
		digest := layerDigestFromBlobPath(blobPath)
		ldir, err := m.layerDirForDigest(digest, blobPath)
		if err != nil {
			return "", err
		}
		lowerDirs = append(lowerDirs, ldir)
	}

	// overlayfs needs at least one lowerdir: an image without layers
	// (FROM scratch) gets a shared empty directory.
	if len(lowerDirs) == 0 {
		empty := filepath.Join(m.base, layersDirName, "empty")
		if err := os.MkdirAll(empty, 0o755); err != nil {
			return "", err
		}
		lowerDirs = append(lowerDirs, empty)
	}

	// overlayfs lowerdir is colon-separated, upper-most layer listed first.
	reversedLowers := reverseStrings(lowerDirs)
	lowerOpt := strings.Join(reversedLowers, ":")
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerOpt, diffDir, workDir)
	if m.opts.Rootless {
		opts = "userxattr," + opts
	}

	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, opts); err != nil {
		return "", fmt.Errorf("overlayfs mount: %w", err)
	}

	return mergedDir, nil
}

// Remove unmounts and cleans up all overlayfs state for containerID.
func (m *Manager) Remove(containerID string) error {
	cDir := filepath.Join(m.base, containerID)
	mergedDir := filepath.Join(cDir, "merged")

	// Unmount - ignore EINVAL (already unmounted).
	if err := syscall.Unmount(mergedDir, syscall.MNT_DETACH); err != nil {
		if err != syscall.EINVAL && err != syscall.ENOENT {
			return fmt.Errorf("overlay unmount %s: %w", containerID, err)
		}
	}

	return os.RemoveAll(cDir)
}

// Unmount detaches the merged mount of containerID but keeps its upper dir,
// so the changes can still be read (e.g. committed as an image layer).
func (m *Manager) Unmount(containerID string) error {
	mergedDir := filepath.Join(m.base, containerID, "merged")
	if err := syscall.Unmount(mergedDir, syscall.MNT_DETACH); err != nil && err != syscall.EINVAL && err != syscall.ENOENT {
		return fmt.Errorf("overlay unmount %s: %w", containerID, err)
	}
	return nil
}

// UpperDir returns the writable layer (overlay upperdir) of containerID.
func (m *Manager) UpperDir(containerID string) string {
	return filepath.Join(m.base, containerID, "diff")
}

// XattrPrefix is the namespace overlayfs uses for its xattrs on this
// manager's mounts ("trusted." rootful, "user." rootless).
func (m *Manager) XattrPrefix() string {
	if m.opts.Rootless {
		return "user."
	}
	return layer.DefaultXattrPrefix
}

// MergedDir returns the merged mount path for a container without mounting.
func (m *Manager) MergedDir(containerID string) string {
	return filepath.Join(m.base, containerID, "merged")
}

// IsMounted returns true if the merged dir has an active overlayfs mount.
func (m *Manager) IsMounted(containerID string) bool {
	mergedDir := filepath.Join(m.base, containerID, "merged")
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), mergedDir)
}

func layerDigestFromBlobPath(blobPath string) string {
	base := filepath.Base(blobPath)
	if len(base) == 64 {
		return "sha256:" + base
	}
	return base
}

func (m *Manager) lockDigest(d string) func() {
	m.dlMu.Lock()
	mu, ok := m.digestLocks[d]
	if !ok {
		mu = &sync.Mutex{}
		m.digestLocks[d] = mu
	}
	m.dlMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// layerDirForDigest returns the shared extracted directory for a layer blob,
// extracting it on first use.
func (m *Manager) layerDirForDigest(digest, blobPath string) (string, error) {
	hex := strings.TrimPrefix(digest, "sha256:")
	if hex == "" || strings.Contains(hex, string(filepath.Separator)) || strings.Contains(hex, "..") {
		return "", fmt.Errorf("invalid layer digest %q", digest)
	}
	layersRoot := filepath.Join(m.base, layersDirName)
	dir := filepath.Join(layersRoot, hex)
	unlock := m.lockDigest(digest)
	defer unlock()

	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, nil
	}
	tmp, err := os.MkdirTemp(layersRoot, ".extract-"+hex[:min(12, len(hex))]+"-")
	if err != nil {
		return "", err
	}
	if err := extractLayer(blobPath, tmp, m.applyOptions()); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("extract layer %s: %w", digest, err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	return dir, nil
}

func (m *Manager) applyOptions() layer.ApplyOptions {
	o := layer.ApplyOptions{Mode: layer.ModeOverlay, Chown: os.Geteuid() == 0}
	if m.opts.Rootless {
		o.XattrPrefix = "user."
	}
	if m.opts.SingleID {
		o.IDMap = func(int, int) (int, int) { return 0, 0 }
	}
	return o
}

// extractLayer applies the layer blob at src into dest as an overlay lowerdir.
func extractLayer(src, dest string, opts layer.ApplyOptions) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = layer.Apply(f, dest, opts)
	return err
}

func reverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
