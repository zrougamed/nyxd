package build

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// cacheEntry is the outcome of a filesystem step: a layer, or nil when the
// step changed nothing.
type cacheEntry struct {
	Layer  *oci.Descriptor `json:"layer,omitempty"`
	DiffID string          `json:"diffID,omitempty"`
}

// cache maps step chain keys to their layers: BaseDir/build/cache/<key>.json.
// Layer blobs live in the image store; an entry whose blob was pruned is a miss.
type cache struct{ dir string }

func (c *cache) get(key string, hasBlob func(string) bool) (cacheEntry, bool) {
	data, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return cacheEntry{}, false
	}
	if e.Layer != nil && !hasBlob(e.Layer.Digest) {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *cache) put(key string, e cacheEntry) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp := filepath.Join(c.dir, key+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.dir, key+".json"))
}
