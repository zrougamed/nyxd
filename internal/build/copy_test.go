package build

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
)

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func copyAll(t *testing.T, ctx, merged string, srcs []string, dest string, destDir bool) {
	t.Helper()
	var sources []copySource
	for _, s := range srcs {
		m, err := expandSource(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		for i := range m {
			if strings.HasSuffix(s, ".tgz") {
				m[i].archive = isArchive(m[i].abs)
			}
		}
		sources = append(sources, m...)
	}
	if len(sources) > 1 || strings.HasSuffix(dest, "/") {
		destDir = true
	}
	for _, s := range sources {
		if err := copyInto(merged, s, dest, destDir, copyOpts{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyDestIsDir(t *testing.T) {
	cases := []struct {
		dest string
		n    int
		want bool
	}{
		{".", 1, true},
		{"./.", 1, true},
		{"/app/.", 1, true},
		{"/app/", 1, true},
		{"/app", 1, false},
		{"file", 1, false},
		{".", 2, true},
		{"/app", 2, true},
	}
	for _, c := range cases {
		if got := copyDestIsDir(c.dest, c.n); got != c.want {
			t.Errorf("copyDestIsDir(%q, %d) = %v, want %v", c.dest, c.n, got, c.want)
		}
	}
}

func TestCopyFileIntoDotWorkdir(t *testing.T) {
	// Mirrors COPY binary . after WORKDIR /app: dest cleans to /app, but must
	// land as /app/<basename>, not replace /app with a file.
	ctx, merged := t.TempDir(), t.TempDir()
	write(t, filepath.Join(ctx, "app"), "binary")
	if err := os.MkdirAll(filepath.Join(merged, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := "/app"
	if !copyDestIsDir(".", 1) {
		t.Fatal("'.' must be a directory destination")
	}
	sources, err := expandSource(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if err := copyInto(merged, sources[0], dest, true, copyOpts{}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(merged, "app"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("/app is not a directory: %v %+v", err, fi)
	}
	if got := read(t, filepath.Join(merged, "app/app")); got != "binary" {
		t.Fatalf("file = %q", got)
	}
}

func TestCopySemantics(t *testing.T) {
	ctx, merged := t.TempDir(), t.TempDir()
	write(t, filepath.Join(ctx, "app/main.go"), "package main")
	write(t, filepath.Join(ctx, "app/sub/x.txt"), "x")
	write(t, filepath.Join(ctx, "a.conf"), "a")
	write(t, filepath.Join(ctx, "b.conf"), "b")
	os.Symlink("main.go", filepath.Join(ctx, "app/link"))
	// Image with a merged-/usr layout.
	os.MkdirAll(filepath.Join(merged, "usr/lib"), 0o755)
	os.Symlink("usr/lib", filepath.Join(merged, "lib"))

	copyAll(t, ctx, merged, []string{"app"}, "/srv", false)            // dir: contents
	copyAll(t, ctx, merged, []string{"a.conf"}, "/etc/renamed", false) // file rename
	copyAll(t, ctx, merged, []string{"a.conf"}, "/etc/conf.d/", false) // into dir
	copyAll(t, ctx, merged, []string{"*.conf"}, "/opt", false)         // wildcard
	copyAll(t, ctx, merged, []string{"app/sub"}, "/lib/sub", false)    // through image symlink

	for p, want := range map[string]string{
		"srv/main.go":       "package main",
		"srv/sub/x.txt":     "x",
		"etc/renamed":       "a",
		"etc/conf.d/a.conf": "a",
		"opt/a.conf":        "a",
		"opt/b.conf":        "b",
		"usr/lib/sub/x.txt": "x",
	} {
		if got := read(t, filepath.Join(merged, p)); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	if l, _ := os.Readlink(filepath.Join(merged, "srv/link")); l != "main.go" {
		t.Errorf("symlink not preserved: %q", l)
	}
	if fi, _ := os.Lstat(filepath.Join(merged, "lib")); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("image /lib symlink was replaced by a directory")
	}
}

func TestCopyLibDirOntoSymlink(t *testing.T) {
	ctx, merged := t.TempDir(), t.TempDir()
	write(t, filepath.Join(ctx, "rootfs/lib/libx.so"), "elf")
	os.MkdirAll(filepath.Join(merged, "usr/lib"), 0o755)
	os.Symlink("usr/lib", filepath.Join(merged, "lib"))
	copyAll(t, ctx, merged, []string{"rootfs"}, "/", false)
	if got := read(t, filepath.Join(merged, "usr/lib/libx.so")); got != "elf" {
		t.Fatalf("libx.so = %q", got)
	}
	if fi, _ := os.Lstat(filepath.Join(merged, "lib")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("/lib symlink replaced")
	}
}

func TestCopySourceCannotEscapeContext(t *testing.T) {
	parent := t.TempDir()
	ctx := filepath.Join(parent, "ctx")
	write(t, filepath.Join(parent, "secret"), "s")
	os.MkdirAll(ctx, 0o755)
	os.Symlink("../secret", filepath.Join(ctx, "rel"))
	os.Symlink("/etc/passwd", filepath.Join(ctx, "abs"))
	for _, src := range []string{"rel", "abs", "../secret", "/etc/passwd"} {
		m, err := expandSource(ctx, src)
		if err == nil {
			for _, s := range m {
				if !within(ctx, s.abs) {
					t.Fatalf("%s resolved outside the context: %s", src, s.abs)
				}
			}
			t.Fatalf("%s: expected not found inside context, got %+v", src, m)
		}
	}
}

func TestAddArchive(t *testing.T) {
	ctx, merged := t.TempDir(), t.TempDir()
	f, _ := os.Create(filepath.Join(ctx, "files.tgz"))
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pkg/README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.Close()
	gz.Close()
	f.Close()
	copyAll(t, ctx, merged, []string{"files.tgz"}, "/opt/", false)
	if got := read(t, filepath.Join(merged, "opt/pkg/README")); got != "hi" {
		t.Fatalf("README = %q", got)
	}
}

func TestAddArchiveChmodChown(t *testing.T) {
	ctx, merged := t.TempDir(), t.TempDir()
	f, _ := os.Create(filepath.Join(ctx, "files.tgz"))
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "pkg/README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	mode := os.FileMode(0o600)
	u := bundle.User{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	sources, err := expandSource(ctx, "files.tgz")
	if err != nil {
		t.Fatal(err)
	}
	sources[0].archive = true
	if err := copyInto(merged, sources[0], "/opt", true, copyOpts{chown: &u, chmod: &mode}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(merged, "opt/pkg/README"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("chmod after ADD archive: %v mode=%v", err, fi)
	}
	if got := read(t, filepath.Join(merged, "opt/pkg/README")); got != "hi" {
		t.Fatalf("README = %q", got)
	}
}

func TestHashSourcesDetectsChanges(t *testing.T) {
	ctx := t.TempDir()
	write(t, filepath.Join(ctx, "d/f"), "1")
	src, _ := expandSource(ctx, "d")
	h1, _ := hashSources(src, false)
	h2, _ := hashSources(src, false)
	write(t, filepath.Join(ctx, "d/f"), "2")
	h3, _ := hashSources(src, false)
	if h1 != h2 || h1 == h3 {
		t.Fatalf("hash stable=%v changed=%v", h1 == h2, h1 != h3)
	}
}
