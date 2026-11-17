package control

import (
	"encoding/json"
	"github.com/zrougamed/nyxd/internal/events"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/logs"
)

// nyx system df / prune: where the disk went, and removing what nothing
// references. Sizes are allocated bytes (st_blocks), hard links counted once.

// buildLeftoverAge keeps build contexts, bundles and unreferenced blobs this
// young: a build or pull in progress is still using them.
const buildLeftoverAge = time.Hour

type dfCategory struct {
	Type        string `json:"type"`
	Count       int    `json:"count"`
	Active      int    `json:"active"`
	Size        int64  `json:"size"`
	Reclaimable int64  `json:"reclaimable"`
}

type dfItem struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Unique int64  `json:"unique,omitempty"` // images: bytes no other image shares
	Active bool   `json:"active"`
	Detail string `json:"detail,omitempty"`
}

type dfResponse struct {
	Categories []dfCategory        `json:"categories"`
	Items      map[string][]dfItem `json:"items"`
}

// diskUsage is the allocated size of path (a file or a tree), skipping
// directories for which skip returns true. seen dedupes hard links.
func diskUsage(path string, skip func(string) bool, seen map[[2]uint64]bool) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skip != nil && p != path && skip(p) {
			return filepath.SkipDir
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		total += allocated(fi, seen)
		return nil
	})
	return total
}

func (s *Server) logDirEntries() map[string]int64 {
	out := map[string]int64{}
	ents, err := os.ReadDir(s.containerLogDir())
	if err != nil {
		return out
	}
	seen := map[[2]uint64]bool{}
	for _, e := range ents {
		name := e.Name()
		i := strings.Index(name, ".log")
		if e.IsDir() || i <= 0 {
			continue
		}
		if rest := name[i+len(".log"):]; rest != "" && !isRotationSuffix(rest) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out[name[:i]] += allocated(fi, seen)
		}
	}
	return out
}

func isRotationSuffix(s string) bool {
	if len(s) < 2 || s[0] != '.' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type volumeDir struct{ name, path string }

func (s *Server) volumeDirs() []volumeDir {
	var out []volumeDir
	root := filepath.Join(s.dataDir, "volumes")
	projects, _ := os.ReadDir(root)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		vols, _ := os.ReadDir(filepath.Join(root, p.Name()))
		for _, v := range vols {
			if v.IsDir() {
				out = append(out, volumeDir{p.Name() + "/" + v.Name(), filepath.Join(root, p.Name(), v.Name())})
			}
		}
	}
	return out
}

// buildLeftovers are build context and bundle directories older than
// buildLeftoverAge (a finished build removes its own).
func (s *Server) buildLeftovers() []string {
	var out []string
	cutoff := time.Now().Add(-buildLeftoverAge)
	for _, sub := range []string{"contexts", "bundles"} {
		ents, _ := os.ReadDir(filepath.Join(s.dataDir, "build", sub))
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
				out = append(out, filepath.Join(s.dataDir, "build", sub, e.Name()))
			}
		}
	}
	return out
}

func (s *Server) buildCacheEntries() []string {
	m, _ := filepath.Glob(filepath.Join(s.dataDir, "build", "cache", "*.json"))
	return m
}

// diskReport gathers nyx system df.
func (s *Server) diskReport() (dfResponse, error) {
	resp := dfResponse{Items: map[string][]dfItem{}}
	seen := map[[2]uint64]bool{}

	running := map[string]bool{}
	for _, id := range s.sup.List() {
		running[id] = true
	}
	exited := map[string]bool{}
	for _, id := range s.sup.ExitedIDs() {
		if !running[id] {
			exited[id] = true
		}
	}

	// Images.
	usage, err := s.store.Usage(s.sup.ImageRefsInUse())
	if err != nil {
		return resp, err
	}
	// Image layers also exist extracted, in the overlay's shared layer store
	// (every container's lowerdirs): counted here, never pruned.
	layerStore := diskUsage(s.sup.LayerStoreDir(), nil, seen)
	resp.Categories = append(resp.Categories, dfCategory{Type: "images", Count: len(usage.Images), Active: usage.Active, Size: usage.Size + layerStore, Reclaimable: usage.Reclaimable})
	for _, im := range usage.Images {
		resp.Items["images"] = append(resp.Items["images"], dfItem{Name: im.Ref, Size: im.Size, Unique: im.Unique, Active: im.InUse})
	}

	// Build cache: step cache entries, layers no image references (cache
	// layers and leftovers), stale build directories.
	bc := dfCategory{Type: "build_cache"}
	for _, f := range s.buildCacheEntries() {
		bc.Count++
		bc.Size += diskUsage(f, nil, seen)
	}
	for _, n := range usage.Unreferenced {
		bc.Size += n
	}
	for _, d := range s.buildLeftovers() {
		bc.Size += diskUsage(d, nil, seen)
	}
	bc.Reclaimable = bc.Size
	resp.Categories = append(resp.Categories, bc)

	// Containers: writable layers. The merged directory is the mounted view
	// of the whole image: never walked.
	ct := dfCategory{Type: "containers", Count: len(running) + len(exited), Active: len(running)}
	skipMerged := func(p string) bool { return filepath.Base(p) == "merged" }
	for _, id := range s.sup.OverlayIDs() {
		size := diskUsage(s.sup.OverlayDir(id), skipMerged, seen)
		ct.Size += size
		item := dfItem{Name: id, Size: size, Active: running[id], Detail: "running"}
		if !running[id] {
			item.Detail = "orphan overlay"
			if s.sup.OverlayMounted(id) {
				item.Detail = "orphan overlay (mounted)"
			} else {
				ct.Reclaimable += size
			}
		}
		resp.Items["containers"] = append(resp.Items["containers"], item)
	}
	for id := range exited {
		resp.Items["containers"] = append(resp.Items["containers"], dfItem{Name: id, Detail: "exited"})
	}
	resp.Categories = append(resp.Categories, ct)

	// Logs.
	lg := dfCategory{Type: "logs"}
	for id, size := range s.logDirEntries() {
		lg.Count++
		lg.Size += size
		if running[id] {
			lg.Active++
		} else {
			lg.Reclaimable += size
		}
		resp.Items["logs"] = append(resp.Items["logs"], dfItem{Name: id, Size: size, Active: running[id]})
	}
	resp.Categories = append(resp.Categories, lg)

	// Volumes: only removed with --volumes; one used by an exited container
	// counts as in use.
	vl := dfCategory{Type: "volumes"}
	for _, v := range s.volumeDirs() {
		size := diskUsage(v.path, nil, seen)
		inUse := s.sup.VolumeInUse(v.path)
		vl.Count++
		vl.Size += size
		if inUse {
			vl.Active++
		} else {
			vl.Reclaimable += size
		}
		resp.Items["volumes"] = append(resp.Items["volumes"], dfItem{Name: v.name, Size: size, Active: inUse, Detail: v.path})
	}
	resp.Categories = append(resp.Categories, vl)

	for _, items := range resp.Items {
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	}
	return resp, nil
}

func (s *Server) handleSystemDF(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil || s.store == nil {
		http.Error(w, "supervisor or image store not available", http.StatusServiceUnavailable)
		return
	}
	resp, err := s.diskReport()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type systemPruneRequest struct {
	Images  bool `json:"images"`  // also images no container uses
	Volumes bool `json:"volumes"` // also volumes no container mounts
	DryRun  bool `json:"dry_run"`
}

type systemPruneResponse struct {
	OK        bool                `json:"ok"`
	DryRun    bool                `json:"dry_run"`
	Removed   map[string][]string `json:"removed"`
	Reclaimed int64               `json:"reclaimed"` // bytes; an estimate with dry_run
}

// handleSystemPrune removes what nothing references: exited containers (and
// their logs), logs and overlays left by containers that no longer exist,
// the build cache and layers no image uses; with images / volumes also
// unused images and volumes. Running containers and what they use are
// never touched.
func (s *Server) handleSystemPrune(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.sup == nil || s.store == nil {
		http.Error(w, "supervisor or image store not available", http.StatusServiceUnavailable)
		return
	}
	var req systemPruneRequest
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	before, err := s.diskReport()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := systemPruneResponse{OK: true, DryRun: req.DryRun, Removed: map[string][]string{}}
	add := func(kind, name string) { out.Removed[kind] = append(out.Removed[kind], name) }
	ctx := r.Context()

	running := map[string]bool{}
	for _, id := range s.sup.List() {
		running[id] = true
	}
	exited := map[string]bool{}
	for _, id := range s.sup.ExitedIDs() {
		if !running[id] {
			exited[id] = true
		}
	}
	// Exited containers (Remove deletes their logs too).
	for id := range exited {
		if !req.DryRun {
			if err := s.sup.Remove(ctx, id); err != nil {
				s.log.Warn("system prune: remove container", "id", id, "err", err)
				continue
			}
		}
		add("containers", id)
	}
	// Logs and overlays of containers that no longer exist.
	for id := range s.logDirEntries() {
		if running[id] || exited[id] {
			continue
		}
		if !req.DryRun {
			logs.Remove(s.containerLogDir(), id)
		}
		add("logs", id)
	}
	for _, id := range s.sup.OverlayIDs() {
		if running[id] || s.sup.OverlayMounted(id) {
			continue
		}
		if !req.DryRun {
			if err := s.sup.RemoveOrphanOverlay(id); err != nil {
				s.log.Warn("system prune: remove overlay", "id", id, "err", err)
				continue
			}
		}
		add("overlays", id)
	}
	// Images no container uses (after the exited containers went).
	if req.Images {
		keep := s.sup.ImageRefsInUse()
		removed, err := s.store.PruneImagesNotIn(keep, req.DryRun)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ref := range removed {
			add("images", ref)
			if !req.DryRun {
				s.imageEvent(events.ImageRemove, ref)
			}
		}
	}
	// Build cache, stale build directories, layers no image uses.
	for _, f := range s.buildCacheEntries() {
		if !req.DryRun {
			_ = os.Remove(f)
		}
		add("build_cache", strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	for _, d := range s.buildLeftovers() {
		if !req.DryRun {
			_ = os.RemoveAll(d)
		}
		add("build_cache", filepath.Base(d))
	}
	if n, _, err := s.store.PruneUnreferencedBlobsOlderThan(buildLeftoverAge, req.DryRun); err != nil {
		s.log.Warn("system prune: blobs", "err", err)
	} else if n > 0 {
		add("layers", strconv.Itoa(n)+" unreferenced")
	}
	// Volumes no container mounts.
	if req.Volumes {
		for _, v := range s.volumeDirs() {
			if s.sup.VolumeInUse(v.path) {
				continue
			}
			if !req.DryRun {
				if err := os.RemoveAll(v.path); err != nil {
					s.log.Warn("system prune: remove volume", "path", v.path, "err", err)
					continue
				}
			}
			add("volumes", v.name)
		}
	}

	if req.DryRun {
		for _, c := range before.Categories {
			if (c.Type == "images" && !req.Images) || (c.Type == "volumes" && !req.Volumes) {
				continue
			}
			out.Reclaimed += c.Reclaimable
		}
	} else if after, err := s.diskReport(); err == nil {
		out.Reclaimed = total(before) - total(after)
		if out.Reclaimed < 0 {
			out.Reclaimed = 0
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func total(r dfResponse) int64 {
	var n int64
	for _, c := range r.Categories {
		n += c.Size
	}
	return n
}
