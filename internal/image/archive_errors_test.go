package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// mkTar is mkArchive with directories: a name ending in / is one.
func mkTar(files ...tarFile) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(f.name, "/") {
			h = &tar.Header{Name: f.name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		tw.WriteHeader(h) //nolint:errcheck
		tw.Write(f.data)  //nolint:errcheck
	}
	tw.Close()
	return &buf
}

func blobFile(data []byte) tarFile { return tarFile{name: blobName(sha(data)), data: data} }

func jsonOf(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gzipLayer(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "f", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}) //nolint:errcheck
	tw.Write([]byte("x"))                                                               //nolint:errcheck
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestLoadOCIArchiveErrors(t *testing.T) {
	layer := gzipLayer(t)
	cfg := jsonOf(t, oci.ImageConfig{OS: "linux", Architecture: runtime.GOARCH, RootFS: oci.RootFS{Type: "layers", DiffIDs: []string{"sha256:x"}}})
	// No mediaType in the manifest: the descriptor's is used.
	man := jsonOf(t, oci.Manifest{SchemaVersion: 2,
		Config: oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: sha(cfg), Size: int64(len(cfg))},
		Layers: []oci.Descriptor{{MediaType: oci.MediaTypeImageLayerGzip, Digest: sha(layer), Size: int64(len(layer))}}})
	desc := func(data []byte, mt, refName string) oci.Descriptor {
		d := oci.Descriptor{MediaType: mt, Digest: sha(data), Size: int64(len(data))}
		if refName != "" {
			d.Annotations = map[string]string{annotationRefName: refName}
		}
		return d
	}
	index := func(ds ...oci.Descriptor) []byte {
		return jsonOf(t, oci.Index{SchemaVersion: 2, MediaType: oci.MediaTypeImageIndex, Manifests: ds})
	}
	blobs := []tarFile{blobFile(layer), blobFile(cfg), blobFile(man)}
	archive := func(idx []byte, extra ...tarFile) *bytes.Buffer {
		return mkTar(append(append([]tarFile{{name: "index.json", data: idx}}, blobs...), extra...)...)
	}

	s, _ := NewStore(t.TempDir())
	refs, err := s.Load(archive(index(desc(man, oci.MediaTypeImageManifest, "v1"), desc(man, oci.MediaTypeImageManifest, "repo/x:2"))))
	if err != nil || len(refs) != 2 || !strings.HasPrefix(refs[0], "nyx-load:") || refs[1] != "repo/x:2" {
		t.Fatalf("refs %v err %v", refs, err)
	}
	if m, _, err := s.LoadImageMeta("repo/x:2"); err != nil || m.MediaType != oci.MediaTypeImageManifest {
		t.Errorf("manifest %+v %v", m, err)
	}

	other := desc(man, oci.MediaTypeImageManifest, "")
	other.Platform = &oci.Platform{OS: "plan9", Architecture: "mips"}
	noMatch := index(other)
	badJSON := []byte("{not json")
	badCfg := []byte("not a config")
	badCfgMan := jsonOf(t, oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{Digest: sha(badCfg)}})
	missingLayerMan := jsonOf(t, oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{Digest: sha(cfg)},
		Layers: []oci.Descriptor{{Digest: sha([]byte("absent"))}}})
	dirLayer := []byte("dir layer")
	dirLayerMan := jsonOf(t, oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{Digest: sha(cfg)},
		Layers: []oci.Descriptor{{Digest: sha(dirLayer)}}})
	forged := []byte("forged layer")
	forgedMan := jsonOf(t, oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{Digest: sha(cfg)},
		Layers: []oci.Descriptor{{Digest: sha(forged)}}})
	mt := oci.MediaTypeImageManifest
	for _, c := range []struct {
		name string
		ar   *bytes.Buffer
		want string
	}{
		{"bad index.json", archive(badJSON), "index.json"},
		{"index.json is a directory", mkTar(tarFile{name: "index.json/"}), "not a regular file"},
		{"manifest missing", archive(index(desc([]byte("nope"), mt, ""))), "load:"},
		{"manifest tampered", archive(index(desc([]byte("other"), mt, "")), tarFile{name: blobName(sha([]byte("other"))), data: man}), "digest mismatch"},
		{"manifest not json", archive(index(desc(badJSON, mt, "")), blobFile(badJSON)), "load: manifest"},
		{"nested index not json", archive(index(desc(badJSON, oci.MediaTypeImageIndex, "")), blobFile(badJSON)), "invalid"},
		{"no platform match", archive(index(desc(noMatch, oci.MediaTypeImageIndex, "")), blobFile(noMatch)), ""},
		{"config not json", archive(index(desc(badCfgMan, mt, "")), blobFile(badCfgMan), blobFile(badCfg)), "load: config"},
		{"layer missing", archive(index(desc(missingLayerMan, mt, "")), blobFile(missingLayerMan)), "load:"},
		{"layer is a directory", archive(index(desc(dirLayerMan, mt, "")), blobFile(dirLayerMan), tarFile{name: blobName(sha(dirLayer)) + "/"}), "not a regular file"},
		{"layer tampered", archive(index(desc(forgedMan, mt, "")), blobFile(forgedMan), tarFile{name: blobName(sha(forged)), data: []byte("swapped")}), "digest mismatch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := NewStore(t.TempDir())
			if _, err := s.Load(c.ar); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestLoadDockerArchiveErrors(t *testing.T) {
	layer := gzipLayer(t)
	cfg := jsonOf(t, oci.ImageConfig{OS: "linux", Architecture: "amd64"})
	entry := func(e dockerArchiveEntry) tarFile {
		return tarFile{name: "manifest.json", data: jsonOf(t, []dockerArchiveEntry{e})}
	}
	s, _ := NewStore(t.TempDir())
	// No RepoTags: a generated name; a gzip layer keeps its media type.
	refs, err := s.Load(mkTar(tarFile{name: "c.json", data: cfg}, tarFile{name: "l.tgz", data: layer},
		entry(dockerArchiveEntry{Config: "c.json", Layers: []string{"l.tgz"}})))
	if err != nil || len(refs) != 1 || !strings.HasPrefix(refs[0], "nyx-load:") {
		t.Fatalf("refs %v err %v", refs, err)
	}
	if m, _, _ := s.LoadImageMeta(refs[0]); m == nil || m.Layers[0].MediaType != oci.MediaTypeImageLayerGzip {
		t.Errorf("layer media type: %+v", m)
	}
	for _, c := range []struct {
		name string
		ar   *bytes.Buffer
		want string
	}{
		{"bad manifest.json", mkTar(tarFile{name: "manifest.json", data: []byte("[")}), "manifest.json"},
		{"config missing", mkTar(entry(dockerArchiveEntry{Config: "none.json"})), "load: config"},
		{"layer missing", mkTar(tarFile{name: "c.json", data: cfg}, entry(dockerArchiveEntry{Config: "c.json", Layers: []string{"none.tar"}})), "load: layer"},
		{"config not json", mkTar(tarFile{name: "c.json", data: []byte("nope")}, entry(dockerArchiveEntry{Config: "c.json"})), "load: config"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := NewStore(t.TempDir())
			if _, err := s.Load(c.ar); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

// failAfter fails the n-th Write and every one after it.
type failAfter struct{ n, calls int }

func (f *failAfter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls >= f.n {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

// Save reports a failure at any point of the archive.
func TestSaveFailsOnEveryWrite(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	seedImage(t, s, "demo/app:1")
	// A manifest stored without mediaType is saved as an OCI manifest.
	m, cfg, err := s.LoadImageMeta("demo/app:1")
	if err != nil {
		t.Fatal(err)
	}
	m.MediaType = ""
	if err := s.SaveImage("demo/app:bare", m, cfg); err != nil {
		t.Fatal(err)
	}
	refs := []string{"demo/app:1", "demo/app:bare"}
	count := &failAfter{n: 1 << 30}
	if err := s.Save(count, refs); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= count.calls; n++ {
		if err := s.Save(&failAfter{n: n}, refs); err == nil {
			t.Errorf("write %d of %d failed, Save succeeded", n, count.calls)
		}
	}
	if err := os.Remove(s.BlobPath(m.Layers[0].Digest)); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&failAfter{n: 1 << 30}, refs); err == nil || !strings.Contains(err.Error(), "missing from the local store") {
		t.Errorf("missing blob: %v", err)
	}
}
