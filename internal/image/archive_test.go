package image

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	src, _ := NewStore(t.TempDir())
	cfgDigest := seedImage(t, src, "demo/app:1")
	if err := src.Tag("demo/app:1", "demo/app:latest"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.Save(&buf, []string{"demo/app:1", "demo/app:latest"}); err != nil {
		t.Fatal(err)
	}

	// The archive carries both an OCI layout and a docker manifest.json.
	names := map[string]bool{}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = true
	}
	for _, n := range []string{"oci-layout", "index.json", "manifest.json", blobName(cfgDigest)} {
		if !names[n] {
			t.Fatalf("archive lacks %s (have %v)", n, names)
		}
	}

	dst, _ := NewStore(t.TempDir())
	refs, err := dst.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(refs, ",") != "demo/app:1,demo/app:latest" {
		t.Fatalf("refs = %v", refs)
	}
	m, _, paths, err := dst.ResolvePulledImage("demo/app:1")
	if err != nil || m.Config.Digest != cfgDigest || len(paths) != 1 {
		t.Fatalf("loaded image: %+v %v %v", m, paths, err)
	}
}

type tarFile struct {
	name, link string
	data       []byte
}

func mkArchive(t *testing.T, files ...tarFile) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if f.link != "" {
			h = &tar.Header{Name: f.name, Linkname: f.link, Typeflag: tar.TypeSymlink, Mode: 0o777}
		}
		tw.WriteHeader(h)
		tw.Write(f.data)
	}
	tw.Close()
	return &buf
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func TestLoadDockerArchive(t *testing.T) {
	var layer bytes.Buffer
	lw := tar.NewWriter(&layer)
	lw.WriteHeader(&tar.Header{Name: "etc/motd", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	lw.Write([]byte("hi"))
	lw.Close()
	cfg, _ := json.Marshal(oci.ImageConfig{OS: "linux", Architecture: "amd64",
		RootFS: oci.RootFS{Type: "layers", DiffIDs: []string{sha(layer.Bytes())}}})
	manifest, _ := json.Marshal([]dockerArchiveEntry{{
		Config: "cfg.json", RepoTags: []string{"legacy:1"}, Layers: []string{"abc/layer.tar"},
	}})
	ar := mkArchive(t,
		tarFile{name: "cfg.json", data: cfg},
		tarFile{name: "abc/layer.tar", data: layer.Bytes()},
		tarFile{name: "manifest.json", data: manifest},
	)
	s, _ := NewStore(t.TempDir())
	refs, err := s.Load(ar)
	if err != nil || len(refs) != 1 || refs[0] != "legacy:1" {
		t.Fatalf("refs %v err %v", refs, err)
	}
	m, _, err := s.LoadImageMeta("legacy:1")
	if err != nil || m.Layers[0].MediaType != oci.MediaTypeImageLayer || m.Layers[0].Digest != sha(layer.Bytes()) {
		t.Fatalf("manifest %+v err %v", m, err)
	}
}

func TestLoadRejectsTamperedAndEscapingArchives(t *testing.T) {
	src, _ := NewStore(t.TempDir())
	seedImage(t, src, "demo/app:1")
	var buf bytes.Buffer
	if err := src.Save(&buf, []string{"demo/app:1"}); err != nil {
		t.Fatal(err)
	}

	// Rewrite the archive with one blob's content changed.
	var tampered bytes.Buffer
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	tw := tar.NewWriter(&tampered)
	done := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		data, _ := io.ReadAll(tr)
		if !done && strings.HasPrefix(h.Name, "blobs/sha256/") && h.Typeflag == tar.TypeReg && len(data) > 10 {
			data[len(data)-1] ^= 0xff
			done = true
		}
		h.Size = int64(len(data))
		tw.WriteHeader(h)
		tw.Write(data)
	}
	tw.Close()
	dst, _ := NewStore(t.TempDir())
	if _, err := dst.Load(&tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered archive: err = %v", err)
	}

	// index.json as a symlink to a host file must not be read.
	evil := mkArchive(t, tarFile{name: "index.json", link: "/etc/passwd"})
	if _, err := dst.Load(evil); err == nil {
		t.Fatal("symlinked index.json accepted")
	}
	if _, err := dst.Load(mkArchive(t, tarFile{name: "readme", data: []byte("x")})); err == nil {
		t.Fatal("non-image archive accepted")
	}
}
