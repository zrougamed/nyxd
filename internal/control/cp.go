package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/layer"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

// nyx cp: GET /v1/containers/{id}/archive?path=P returns P as a tar stream
// (its top-level entry named after P), PUT extracts one at P. Everything is
// read and written through os.Root on the container's root filesystem (or a
// bind-mounted volume's source), so a symlink inside the container cannot
// lead outside it.

var errCopyPath = errors.New("invalid path")

// copyLocation is where a container path lives on the daemon's side: rel
// inside the directory base.
type copyLocation struct {
	base string
	rel  string
}

// locateCopyPath maps an absolute container path to the host directory that
// holds it: the bind mount with the longest destination prefix, else the
// root filesystem. Kernel filesystems and non-bind mounts (tmpfs) have no
// host files to copy.
func locateCopyPath(rootFS string, mounts []bundle.Mount, p string) (copyLocation, error) {
	if !strings.HasPrefix(p, "/") {
		return copyLocation{}, fmt.Errorf("%w %q: must be absolute", errCopyPath, p)
	}
	p = path.Clean(p)
	for _, virt := range []string{"/proc", "/sys", "/dev"} {
		if p == virt || strings.HasPrefix(p, virt+"/") {
			return copyLocation{}, fmt.Errorf("%w %q: %s is a kernel filesystem", errCopyPath, p, virt)
		}
	}
	var best *bundle.Mount
	for i := range mounts {
		d := path.Clean(mounts[i].Destination)
		if p != d && !strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			continue
		}
		if best == nil || len(d) > len(path.Clean(best.Destination)) {
			best = &mounts[i]
		}
	}
	if best == nil {
		return copyLocation{base: rootFS, rel: strings.TrimPrefix(p, "/")}, nil
	}
	isBind := best.Type == "bind" || slices.Contains(best.Options, "bind") || slices.Contains(best.Options, "rbind")
	if !isBind || best.Source == "" {
		return copyLocation{}, fmt.Errorf("%w %q: on a %s mount, not stored on the host", errCopyPath, p, best.Type)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(p, path.Clean(best.Destination)), "/")
	if fi, err := os.Stat(best.Source); err == nil && !fi.IsDir() {
		// A single bind-mounted file: its directory is the base.
		return copyLocation{base: filepath.Dir(best.Source), rel: filepath.Base(best.Source)}, nil
	}
	return copyLocation{base: best.Source, rel: rel}, nil
}

// resolveInRoot follows symlinks in rel the way the container would see
// them: absolute targets restart from the root, ".." stops at it. The last
// component is followed only with followLast. A missing last component is
// returned as is (a copy destination may not exist yet).
func resolveInRoot(root *os.Root, rel string, followLast bool) (string, error) {
	pending := splitPath(rel)
	var done []string
	for hops := 0; len(pending) > 0; {
		comp := pending[0]
		pending = pending[1:]
		if comp == ".." {
			if len(done) > 0 {
				done = done[:len(done)-1]
			}
			continue
		}
		cand := path.Join(append(append([]string{}, done...), comp)...)
		fi, err := root.Lstat(cand)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && len(pending) == 0 {
				return cand, nil
			}
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 || (len(pending) == 0 && !followLast) {
			done = append(done, comp)
			continue
		}
		if hops++; hops > 40 {
			return "", fmt.Errorf("%s: too many levels of symbolic links", cand)
		}
		target, err := root.Readlink(cand)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(target, "/") {
			done = nil
		}
		pending = append(splitPath(target), pending...)
	}
	if len(done) == 0 {
		return ".", nil
	}
	return path.Join(done...), nil
}

func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// copyTarget resolves the id and path of a cp request; it writes the error
// response itself and returns ok=false on failure.
func (s *Server) copyTarget(w http.ResponseWriter, r *http.Request) (id, p string, root *os.Root, rel string, ok bool) {
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	id, err := s.resolveContainerID(r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	p = r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	rootFS, mounts, err := s.sup.ContainerFS(id)
	switch {
	case errors.Is(err, supervisor.ErrNotRunning):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, supervisor.ErrNoContainer):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	loc, err := locateCopyPath(rootFS, mounts, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if root, err = os.OpenRoot(loc.base); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	return id, path.Clean(p), root, loc.rel, true
}

func copyPathError(w http.ResponseWriter, p string, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, fmt.Sprintf("%s: no such file or directory in the container", p), http.StatusNotFound)
		return
	}
	http.Error(w, fmt.Sprintf("%s: %v", p, err), http.StatusBadRequest)
}

func (s *Server) handleArchiveGet(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	id, p, root, rel, ok := s.copyTarget(w, r)
	if !ok {
		return
	}
	defer root.Close()
	resolved, err := resolveInRoot(root, rel, false)
	if err == nil {
		_, err = root.Lstat(resolved)
	}
	if err != nil {
		copyPathError(w, p, err)
		return
	}
	name := path.Base(p)
	if p == "/" {
		name = id
	}
	w.Header().Set("Content-Type", "application/x-tar")
	if err := layer.WriteTree(w, root, resolved, name, nil); err != nil {
		// Headers are gone: the client sees a truncated archive.
		s.log.Warn("cp from container", "id", id, "path", p, "err", err)
	}
}

func (s *Server) handleArchivePut(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	id, p, root, rel, ok := s.copyTarget(w, r)
	if !ok {
		return
	}
	defer root.Close()
	dest, err := resolveInRoot(root, rel, true)
	if err != nil {
		copyPathError(w, p, err)
		return
	}
	raw := r.URL.Query().Get("raw") == "1" // a tar stream from stdin: extract as is
	dir := dest
	var body io.Reader = r.Body
	if fi, err := root.Stat(dest); err != nil || !fi.IsDir() {
		if raw {
			http.Error(w, fmt.Sprintf("%s: a tar stream needs an existing directory as destination", p), http.StatusBadRequest)
			return
		}
		if dest == "." {
			copyPathError(w, p, fs.ErrNotExist)
			return
		}
		// Copy to a new (or replaced) name: extract into the parent with the
		// archive's top-level entry renamed.
		dir = path.Dir(dest)
		if fi, err := root.Stat(dir); err != nil || !fi.IsDir() {
			copyPathError(w, path.Dir(p), fs.ErrNotExist)
			return
		}
		body = layer.RenameTop(r.Body, path.Base(dest))
	}
	sub, err := root.OpenRoot(dir)
	if err != nil {
		copyPathError(w, p, err)
		return
	}
	defer sub.Close()
	if _, err := layer.ApplyRoot(body, sub, layer.ApplyOptions{Mode: layer.ModeFlatten, Literal: true, Chown: true}); err != nil {
		s.log.Warn("cp into container", "id", id, "path", p, "err", err)
		http.Error(w, fmt.Sprintf("copy into %s: %v", p, err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "path": p})
}
