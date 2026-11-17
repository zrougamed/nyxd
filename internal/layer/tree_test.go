package layer

import (
	"archive/tar"
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func names(t *testing.T, b []byte) []string {
	t.Helper()
	var out []string
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			sort.Strings(out)
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

func TestWriteTreeApplyRootRoundTrip(t *testing.T) {
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "conf", "sub"), 0o755))
	must(os.WriteFile(filepath.Join(src, "conf", "a.txt"), []byte("hello"), 0o640))
	must(os.WriteFile(filepath.Join(src, "conf", "sub", ".wh.keep"), []byte("not a whiteout"), 0o600))
	must(os.Symlink("../outside", filepath.Join(src, "conf", "link")))
	root, err := os.OpenRoot(src)
	must(err)
	defer root.Close()

	var buf bytes.Buffer
	must(WriteTree(&buf, root, "conf", "conf", nil))
	got := strings.Join(names(t, buf.Bytes()), " ")
	if got != "conf/ conf/a.txt conf/link conf/sub/ conf/sub/.wh.keep" {
		t.Fatalf("entries: %s", got)
	}

	dst := t.TempDir()
	droot, err := os.OpenRoot(dst)
	must(err)
	defer droot.Close()
	_, err = ApplyRoot(RenameTop(bytes.NewReader(buf.Bytes()), "renamed"), droot, ApplyOptions{Mode: ModeFlatten, Literal: true})
	must(err)
	if b, _ := os.ReadFile(filepath.Join(dst, "renamed", "a.txt")); string(b) != "hello" {
		t.Fatalf("a.txt: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "renamed", "sub", ".wh.keep")); string(b) != "not a whiteout" {
		t.Fatalf(".wh. name must stay a file: %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(dst, "renamed", "link")); l != "../outside" {
		t.Fatalf("symlink stored as is: %q", l)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "renamed", "a.txt")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}

	// A single file, written under another name.
	buf.Reset()
	must(WriteTree(&buf, root, "conf/a.txt", "b.txt", &Owner{}))
	if got := names(t, buf.Bytes()); len(got) != 1 || got[0] != "b.txt" {
		t.Fatalf("file entry: %v", got)
	}
}

// A hostile archive cannot write outside the destination: not with "..",
// not through a symlink it planted first.
func TestApplyRootLiteralConfined(t *testing.T) {
	outer := t.TempDir()
	dst := filepath.Join(outer, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.WriteHeader(&tar.Header{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: outer})
	_ = tw.WriteHeader(&tar.Header{Name: "evil/pwned.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	root, _ := os.OpenRoot(dst)
	defer root.Close()
	_, err := ApplyRoot(&buf, root, ApplyOptions{Mode: ModeFlatten, Literal: true})
	if _, statErr := os.Stat(filepath.Join(outer, "pwned.txt")); statErr == nil {
		t.Fatal("wrote through a planted symlink")
	}
	if _, statErr := os.Stat(filepath.Join(outer, "escape.txt")); statErr == nil {
		t.Fatal(`".." escaped the destination`)
	}
	if err == nil {
		t.Fatal("writing through a symlink that leaves the root should fail")
	}
}

func TestRenameTopHardlinkAndBadInput(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "old/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "old/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: "old/h", Typeflag: tar.TypeLink, Linkname: "old/f"})
	_ = tw.Close()
	b, err := io.ReadAll(RenameTop(&buf, "new"))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(b))
	var got []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		got = append(got, h.Name+">"+h.Linkname)
	}
	if strings.Join(got, " ") != "new/> new/f> new/h>new/f" {
		t.Fatalf("renamed: %v", got)
	}
	if _, err := io.ReadAll(RenameTop(strings.NewReader("not a tar archive at all, really"), "x")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestWriteTreeSpecialEntries(t *testing.T) {
	dir := t.TempDir()
	if err := mkfifoForTest(filepath.Join(dir, "pipe")); err != nil {
		t.Skip(err)
	}
	if l, err := net.Listen("unix", filepath.Join(dir, "sock")); err == nil {
		defer l.Close()
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var buf bytes.Buffer
	// The root itself ("" or "."), named "all": sockets are skipped, a fifo
	// is stored as a header only.
	if err := WriteTree(&buf, root, "", "all", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, buf.Bytes()), " "); got != "all/ all/f all/pipe" {
		t.Fatalf("entries: %s", got)
	}
	if err := WriteTree(io.Discard, root, "missing", "m", nil); err == nil {
		t.Fatal("missing path accepted")
	}
}
