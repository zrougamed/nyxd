package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestFetchRootfsLocal(t *testing.T) {
	dir := t.TempDir()
	tarData := []byte("pretend this is a tar stream")
	for name, content := range map[string][]byte{
		"plain.tar":  tarData,
		"rootfs.tgz": gzipBytes(t, tarData),
	} {
		src := filepath.Join(dir, name)
		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, name+".out")
		if err := fetchRootfs(src, dst); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(dst)
		if !bytes.Equal(got, tarData) {
			t.Errorf("%s: got %q, want %q", name, got, tarData)
		}
	}
}

func TestFetchRootfsDownload(t *testing.T) {
	tarData := []byte("downloaded tar stream")
	gz := gzipBytes(t, tarData)
	published := sumHex(gz)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good.tar.gz", "/tampered.tar.gz":
			w.Write(gz)
		case "/good.tar.gz.sha256":
			w.Write([]byte(strings.ToUpper(published) + "  good.tar.gz\n"))
		case "/tampered.tar.gz.sha256":
			w.Write([]byte(sumHex([]byte("something else")) + "  tampered.tar.gz\n"))
		case "/garbage.tar.gz.sha256":
			w.Write([]byte("not a checksum\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()

	dst := filepath.Join(dir, "good.tar")
	if err := fetchRootfs(srv.URL+"/good.tar.gz", dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, tarData) {
		t.Errorf("got %q, want %q", got, tarData)
	}
	if _, err := os.Stat(dst + ".download"); !os.IsNotExist(err) {
		t.Errorf("temporary download left behind: %v", err)
	}

	for name, wantErr := range map[string]string{
		"tampered.tar.gz": "does not match",
		"garbage.tar.gz":  "unexpected content",
		"missing.tar.gz":  "404",
	} {
		dst := filepath.Join(dir, name+".tar")
		err := fetchRootfs(srv.URL+"/"+name, dst)
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, wantErr)
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Errorf("%s: output written despite failure", name)
		}
		if _, err := os.Stat(dst + ".download"); !os.IsNotExist(err) {
			t.Errorf("%s: temporary download left behind", name)
		}
	}
}
