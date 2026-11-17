package image

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// ImageUsage is one stored image's disk use for nyx system df -v.
type ImageUsage struct {
	Ref    string `json:"ref"`
	Size   int64  `json:"size"`   // all its blobs
	Unique int64  `json:"unique"` // blobs no other image uses
	InUse  bool   `json:"in_use"`
}

// StoreUsage is the image store's disk use. Blobs shared between images are
// counted once.
type StoreUsage struct {
	Images      []ImageUsage
	Active      int   // images used by a container (running or exited)
	Size        int64 // blobs referenced by at least one image
	Reclaimable int64 // blobs referenced only by images no container uses
	// Unreferenced maps blobs no image references (build cache layers,
	// leftovers of removed images) to their size.
	Unreferenced map[string]int64
}

// Usage reports disk use. keep are the references containers use (matched
// by storage location, like PruneImagesNotIn).
func (s *Store) Usage(keep map[string]struct{}) (StoreUsage, error) {
	imgs, err := s.storedImages()
	if err != nil {
		return StoreUsage{}, err
	}
	kept := make(map[string]bool, len(keep))
	for ref := range keep {
		kept[s.imageDir(ref)] = true
	}
	blobSize := map[string]int64{}
	sizeOf := func(d string) int64 {
		if n, ok := blobSize[d]; ok {
			return n
		}
		var n int64
		if fi, err := os.Stat(s.BlobPath(d)); err == nil {
			n = fi.Size()
		}
		blobSize[d] = n
		return n
	}
	users := map[string]int{}       // digest -> images referencing it
	usedByKept := map[string]bool{} // digest -> referenced by an image in use
	digestsOf := make([][]string, len(imgs))
	for i, im := range imgs {
		digestsOf[i] = manifestDigests(filepath.Join(im.dir, "manifest.json"))
		for _, d := range digestsOf[i] {
			users[d]++
			if kept[im.dir] {
				usedByKept[d] = true
			}
		}
	}
	var u StoreUsage
	for i, im := range imgs {
		iu := ImageUsage{Ref: im.ref, InUse: kept[im.dir]}
		for _, d := range digestsOf[i] {
			n := sizeOf(d)
			iu.Size += n
			if users[d] == 1 {
				iu.Unique += n
			}
		}
		if iu.InUse {
			u.Active++
		}
		u.Images = append(u.Images, iu)
	}
	for d := range users {
		u.Size += sizeOf(d)
		if !usedByKept[d] {
			u.Reclaimable += sizeOf(d)
		}
	}
	u.Unreferenced = map[string]int64{}
	ents, _ := os.ReadDir(filepath.Join(s.root, "blobs", "sha256"))
	for _, e := range ents {
		if e.IsDir() || len(e.Name()) != 64 {
			continue
		}
		d := "sha256:" + e.Name()
		if users[d] == 0 {
			u.Unreferenced[d] = sizeOf(d)
		}
	}
	sort.Slice(u.Images, func(i, j int) bool { return u.Images[i].Ref < u.Images[j].Ref })
	return u, nil
}

// manifestDigests is the config and layer digests of a stored manifest.
func manifestDigests(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m oci.Manifest
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range append([]oci.Descriptor{m.Config}, m.Layers...) {
		if d.Digest != "" && !seen[d.Digest] {
			seen[d.Digest] = true
			out = append(out, d.Digest)
		}
	}
	return out
}

// PruneUnreferencedBlobsOlderThan is PruneUnreferencedBlobs that keeps blobs
// written in the last age: a pull or build in progress writes its layers
// before the manifest that references them. It returns the bytes freed.
func (s *Store) PruneUnreferencedBlobsOlderThan(age time.Duration, dryRun bool) (int, int64, error) {
	refDigests, err := s.collectReferencedDigests()
	if err != nil {
		return 0, 0, err
	}
	blobDir := filepath.Join(s.root, "blobs", "sha256")
	ents, err := os.ReadDir(blobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	cutoff := time.Now().Add(-age)
	removed, freed := 0, int64(0)
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || len(name) != 64 || strings.HasSuffix(name, ".partial") || refDigests["sha256:"+name] {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue
		}
		if !dryRun {
			if err := os.Remove(filepath.Join(blobDir, name)); err != nil && !os.IsNotExist(err) {
				return removed, freed, err
			}
		}
		removed++
		freed += fi.Size()
	}
	return removed, freed, nil
}
