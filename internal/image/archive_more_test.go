package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// ociArchive builds an OCI layout archive with one image; wrap controls
// how index.json points at the manifest.
func ociArchive(t *testing.T, gz bool, wrap func(manifest oci.Descriptor, put func([]byte) string) []oci.Descriptor, extra ...tarFile) *bytes.Buffer {
	t.Helper()
	blobs := map[string][]byte{}
	put := func(b []byte) string { d := sha(b); blobs[d] = b; return d }
	var layer bytes.Buffer
	lw := tar.NewWriter(&layer)
	lw.WriteHeader(&tar.Header{Name: "f", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}) //nolint:errcheck
	lw.Write([]byte("x"))                                                               //nolint:errcheck
	lw.Close()
	lb := layer.Bytes()
	if gz {
		var z bytes.Buffer
		zw := gzip.NewWriter(&z)
		zw.Write(lb) //nolint:errcheck
		zw.Close()
		lb = z.Bytes()
	}
	cfg, _ := json.Marshal(oci.ImageConfig{OS: "linux", Architecture: runtime.GOARCH, RootFS: oci.RootFS{Type: "layers", DiffIDs: []string{sha(layer.Bytes())}}})
	m, _ := json.Marshal(oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeImageManifest,
		Config: oci.Descriptor{Digest: put(cfg), Size: int64(len(cfg))},
		Layers: []oci.Descriptor{{Digest: put(lb), Size: int64(len(lb))}}})
	md := oci.Descriptor{MediaType: oci.MediaTypeImageManifest, Digest: put(m), Size: int64(len(m))}
	idx, _ := json.Marshal(oci.Index{SchemaVersion: 2, Manifests: wrap(md, put)})
	files := []tarFile{{name: "oci-layout", data: []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {name: "index.json", data: idx}}
	for d, b := range blobs {
		files = append(files, tarFile{name: "blobs/sha256/" + strings.TrimPrefix(d, "sha256:"), data: b})
	}
	return mkArchive(t, append(files, extra...)...)
}

func TestLoadOCIVariants(t *testing.T) {
	direct := func(ann map[string]string) func(oci.Descriptor, func([]byte) string) []oci.Descriptor {
		return func(md oci.Descriptor, _ func([]byte) string) []oci.Descriptor {
			md.Annotations = ann
			return []oci.Descriptor{md}
		}
	}
	for name, c := range map[string]struct {
		ar   *bytes.Buffer
		want string // ref prefix, or "err:<substring>"
	}{
		"containerd name":    {ociArchive(t, true, direct(map[string]string{annotationContainerdRef: "named/app:1"})), "named/app:1"},
		"ref.name with repo": {ociArchive(t, false, direct(map[string]string{annotationRefName: "repo/app:2"})), "repo/app:2"},
		"bare tag":           {ociArchive(t, false, direct(map[string]string{annotationRefName: "3"})), "nyx-load:"},
		"nested index": {ociArchive(t, true, func(md oci.Descriptor, put func([]byte) string) []oci.Descriptor {
			md.Platform = &oci.Platform{OS: "linux", Architecture: runtime.GOARCH}
			other := oci.Descriptor{Digest: "sha256:other", Platform: &oci.Platform{OS: "linux", Architecture: "s390x"}}
			inner, _ := json.Marshal(oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{other, md}})
			return []oci.Descriptor{{MediaType: oci.MediaTypeImageIndex, Digest: put(inner), Size: int64(len(inner))}}
		}), "nyx-load:"},
		"nested index, no platform": {ociArchive(t, true, func(md oci.Descriptor, put func([]byte) string) []oci.Descriptor {
			inner, _ := json.Marshal(oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{md}})
			return []oci.Descriptor{{MediaType: oci.MediaTypeImageIndex, Digest: put(inner), Size: int64(len(inner))}}
		}), "err:no manifest"},
		"index loop": {ociArchive(t, true, func(md oci.Descriptor, put func([]byte) string) []oci.Descriptor {
			self := oci.Descriptor{MediaType: oci.MediaTypeImageIndex, Platform: &oci.Platform{OS: "linux", Architecture: runtime.GOARCH}}
			var d oci.Descriptor
			for i := 0; i < 6; i++ {
				self.Digest = d.Digest
				inner, _ := json.Marshal(oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{func() oci.Descriptor {
					if i == 0 {
						return md
					}
					x := self
					return x
				}()}})
				d = oci.Descriptor{MediaType: oci.MediaTypeImageIndex, Digest: put(inner), Size: int64(len(inner)), Platform: self.Platform}
			}
			return []oci.Descriptor{d}
		}), "err:nesting too deep"},
		"broken index.json": {mkArchive(t, tarFile{name: "oci-layout", data: []byte("{}")}, tarFile{name: "index.json", data: []byte("{")}), "err:index.json"},
		"missing manifest blob": {ociArchive(t, true, func(md oci.Descriptor, _ func([]byte) string) []oci.Descriptor {
			md.Digest = sha([]byte("absent"))
			return []oci.Descriptor{md}
		}), "err:"},
		"manifest blob is a dir": {ociArchive(t, true, func(md oci.Descriptor, _ func([]byte) string) []oci.Descriptor {
			md.Digest = sha([]byte("dir"))
			return []oci.Descriptor{md}
		}, tarFile{name: "blobs/sha256/" + strings.TrimPrefix(sha([]byte("dir")), "sha256:") + "/x", data: []byte("x")}), "err:"},
		"corrupt manifest": {ociArchive(t, true, func(md oci.Descriptor, put func([]byte) string) []oci.Descriptor {
			md.Digest = put([]byte("{not json"))
			return []oci.Descriptor{md}
		}), "err:manifest"},
	} {
		s := newStore(t)
		refs, err := s.Load(c.ar)
		if strings.HasPrefix(c.want, "err:") {
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(c.want, "err:")) {
				t.Errorf("%s: %v %v", name, refs, err)
			}
			continue
		}
		if err != nil || len(refs) != 1 || !strings.HasPrefix(refs[0], c.want) {
			t.Errorf("%s: %v %v", name, refs, err)
		}
	}
}

func TestLoadDockerVariants(t *testing.T) {
	var layer bytes.Buffer
	lw := tar.NewWriter(&layer)
	lw.WriteHeader(&tar.Header{Name: "f", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}) //nolint:errcheck
	lw.Write([]byte("x"))                                                               //nolint:errcheck
	lw.Close()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(layer.Bytes()) //nolint:errcheck
	zw.Close()
	cfg, _ := json.Marshal(oci.ImageConfig{OS: "linux", Architecture: "amd64"})
	entry := func(tags []string, layers ...string) []byte {
		b, _ := json.Marshal([]dockerArchiveEntry{{Config: "c.json", RepoTags: tags, Layers: layers}})
		return b
	}
	for name, c := range map[string]struct {
		files []tarFile
		want  string
	}{
		"untagged, gzip layer": {[]tarFile{{name: "c.json", data: cfg}, {name: "l.tar.gz", data: gz.Bytes()}, {name: "manifest.json", data: entry(nil, "l.tar.gz")}}, "nyx-load:"},
		"broken manifest.json": {[]tarFile{{name: "manifest.json", data: []byte("{")}}, "err:manifest.json"},
		"missing config":       {[]tarFile{{name: "manifest.json", data: entry(nil)}}, "err:config"},
		"missing layer":        {[]tarFile{{name: "c.json", data: cfg}, {name: "manifest.json", data: entry(nil, "nope.tar")}}, "err:layer"},
		"config not json":      {[]tarFile{{name: "c.json", data: []byte("{")}, {name: "manifest.json", data: entry(nil)}}, "err:config"},
		"neither layout":       {[]tarFile{{name: "x", data: []byte("x")}}, "err:"},
	} {
		s := newStore(t)
		refs, err := s.Load(mkArchive(t, c.files...))
		if strings.HasPrefix(c.want, "err:") {
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(c.want, "err:")) {
				t.Errorf("%s: %v %v", name, refs, err)
			}
			continue
		}
		if err != nil || len(refs) != 1 || !strings.HasPrefix(refs[0], c.want) {
			t.Errorf("%s: %v %v", name, refs, err)
		}
	}
	if _, err := newStore(t).Load(strings.NewReader("not a tar")); err == nil {
		t.Error("garbage")
	}
}

func TestSaveErrors(t *testing.T) {
	s := newStore(t)
	var b bytes.Buffer
	if err := s.Save(&b, nil); err == nil {
		t.Error("no refs")
	}
	if err := s.Save(&b, []string{"nope:1"}); err == nil {
		t.Error("unknown ref")
	}
	seedImage(t, s, "a/b:1")
	m, _, _ := s.LoadImageMeta("a/b:1")
	_ = removeFile(s.BlobPath(m.Layers[0].Digest))
	if err := s.Save(&b, []string{"a/b:1"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("missing blob: %v", err)
	}
	if err := s.Save(failWriter{}, []string{"a/b:1"}); err == nil {
		t.Error("write failure")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errFail }

var errFail = &failErr{}

type failErr struct{}

func (*failErr) Error() string { return "write failed" }

func removeFile(p string) error { return os.Remove(p) }
