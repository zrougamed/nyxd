package image

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestAtomicWriteConcurrentSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ref")
	var fails atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("writer-%d\n", i))
			if err := atomicWrite(path, data); err != nil {
				fails.Add(1)
				t.Errorf("atomicWrite: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if fails.Load() != 0 {
		t.Fatalf("%d concurrent atomicWrite failures", fails.Load())
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(b, []byte("writer-")) {
		t.Fatalf("final file: %q %v", b, err)
	}
	// No leftover fixed-name temp from the old path+".tmp" scheme.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stale %s.tmp: %v", path, err)
	}
}

func TestSaveImageConcurrentSameTag(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: "amd64"}
	var fails atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := *cfg
			if err := s.SaveImage("octoharvest-viewing:latest", m, &c); err != nil {
				fails.Add(1)
				t.Errorf("SaveImage: %v", err)
			}
		}()
	}
	wg.Wait()
	if fails.Load() != 0 {
		t.Fatalf("%d SaveImage failures (rename race)", fails.Load())
	}
	if _, _, err := s.LoadImageMeta("octoharvest-viewing:latest"); err != nil {
		t.Fatal(err)
	}
}
