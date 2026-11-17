//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestContainerIDsSkipsInternalDirs(t *testing.T) {
	base := t.TempDir()
	m, err := NewManager(base, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"web", "job", "_cache"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "stray-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ids := m.ContainerIDs()
	sort.Strings(ids)
	if strings.Join(ids, ",") != "job,web" {
		t.Fatalf("ids: %v", ids)
	}
	if !m.IsReserved("_layers") || !m.IsReserved("_cache") || m.IsReserved("web") {
		t.Fatal("reserved names")
	}
	if m.LayerStoreDir() != filepath.Join(base, "_layers") {
		t.Fatal(m.LayerStoreDir())
	}
	if (&Manager{base: filepath.Join(base, "missing")}).ContainerIDs() != nil {
		t.Fatal("missing base")
	}
}
