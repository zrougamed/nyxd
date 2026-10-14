package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/bundle"
)

func TestCopyHelpersEdgeCases(t *testing.T) {
	ctx, merged := t.TempDir(), t.TempDir()
	write(t, filepath.Join(ctx, "a.txt"), "a")
	write(t, filepath.Join(ctx, "dir", "b.txt"), "b")
	write(t, filepath.Join(merged, "etc", "file"), "x")

	if _, err := expandSource(ctx, "[bad"); err == nil {
		t.Error("bad glob")
	}
	if _, err := expandSource(ctx, "*.none"); err == nil || !strings.Contains(err.Error(), "no source files") {
		t.Errorf("glob without matches: %v", err)
	}
	for raw, want := range map[string]string{"http://h/": "download", "http://h/x/app.tgz?v=1": "app.tgz", "http://h/%zz": "download"} {
		if got := urlFileName(raw); got != want {
			t.Errorf("urlFileName(%q) = %q", raw, got)
		}
	}
	if isArchive(filepath.Join(ctx, "missing")) {
		t.Error("missing file is an archive")
	}
	if err := extractArchive(filepath.Join(ctx, "missing"), merged, copyOpts{}); err == nil {
		t.Error("extract of a missing archive")
	}

	src, _ := expandSource(ctx, "dir")
	file, _ := expandSource(ctx, "a.txt")
	// A file where a directory is needed.
	if err := copyInto(merged, src[0], "/etc/file", true, copyOpts{}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("dir onto a file: %v", err)
	}
	if err := copyInto(merged, copySource{abs: file[0].abs, archive: true}, "/etc/file", true, copyOpts{}); err == nil {
		t.Error("archive onto a file")
	}
	if err := copyInto(merged, file[0], "/etc/file/x", false, copyOpts{}); err == nil {
		t.Error("file under a file")
	}
	// Ownership is requested; unprivileged runs keep their own.
	u := bundle.User{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	mode := os.FileMode(0o640)
	if err := copyInto(merged, src[0], "/new/dir", true, copyOpts{chown: &u, chmod: &mode}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(merged, "new", "dir", "b.txt")); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("chmod: %v %v", fi, err)
	}
	if err := copyTree(filepath.Join(ctx, "dir"), t.TempDir(), merged, copyOpts{}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("destination outside the root: %v", err)
	}
	if err := copyEntry(filepath.Join(ctx, "missing"), filepath.Join(merged, "m"), merged, copyOpts{}, false); err == nil {
		t.Error("copy of a missing source")
	}
	if err := copyFileData(filepath.Join(ctx, "missing"), filepath.Join(merged, "m")); err == nil {
		t.Error("copyFileData of a missing source")
	}
	if err := copyFileData(filepath.Join(ctx, "a.txt"), filepath.Join(merged, "etc", "file")); err == nil {
		t.Error("copyFileData over an existing file")
	}
}

// Unreadable sources and unwritable destinations are errors, not skips.
func TestCopyPermissionErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	ctx, merged := t.TempDir(), t.TempDir()
	write(t, filepath.Join(ctx, "secret", "f"), "s")
	write(t, filepath.Join(ctx, "locked", "f"), "l")
	write(t, filepath.Join(ctx, "a.txt"), "a")
	os.Symlink("a.txt", filepath.Join(ctx, "link")) //nolint:errcheck
	write(t, filepath.Join(merged, "ro", "old"), "o")
	os.Mkdir(filepath.Join(merged, "ro", "olddir"), 0o755) //nolint:errcheck
	for _, p := range []string{filepath.Join(ctx, "secret", "f")} {
		os.Chmod(p, 0)                           //nolint:errcheck
		t.Cleanup(func() { os.Chmod(p, 0o644) }) //nolint:errcheck
	}
	for _, p := range []string{filepath.Join(ctx, "locked"), filepath.Join(merged, "ro")} {
		os.Chmod(p, 0o500)                       //nolint:errcheck
		t.Cleanup(func() { os.Chmod(p, 0o755) }) //nolint:errcheck
	}
	os.Chmod(filepath.Join(ctx, "locked"), 0) //nolint:errcheck

	secret := copySource{abs: filepath.Join(ctx, "secret"), rel: "secret", isDir: true}
	if _, err := hashSources([]copySource{secret}, false); err == nil {
		t.Error("hash of an unreadable file")
	}
	locked := copySource{abs: filepath.Join(ctx, "locked"), rel: "locked", isDir: true}
	if _, err := hashSources([]copySource{locked}, false); err == nil {
		t.Error("hash of an unreadable directory")
	}
	if _, err := expandSource(ctx, "locked/f"); err == nil || strings.Contains(err.Error(), "not found") {
		t.Errorf("stat in an unreadable directory: %v", err)
	}
	if err := copyTree(filepath.Join(ctx, "locked"), filepath.Join(merged, "t"), merged, copyOpts{}); err == nil {
		t.Error("copy of an unreadable directory")
	}
	if err := copyTree(filepath.Join(ctx, "secret"), merged, merged, copyOpts{}); err == nil {
		t.Error("copy of an unreadable file")
	}

	ro := filepath.Join(merged, "ro")
	a := filepath.Join(ctx, "a.txt")
	for name, err := range map[string]error{
		"new file":          copyEntry(a, filepath.Join(ro, "new"), merged, copyOpts{}, false),
		"replace file":      copyEntry(a, filepath.Join(ro, "old"), merged, copyOpts{}, false),
		"replace directory": copyEntry(a, filepath.Join(ro, "olddir"), merged, copyOpts{}, false),
		"new directory":     copyEntry(filepath.Join(ctx, "secret"), filepath.Join(ro, "d"), merged, copyOpts{}, false),
		"new symlink":       copyEntry(filepath.Join(ctx, "link"), filepath.Join(ro, "l"), merged, copyOpts{}, false),
	} {
		if err == nil {
			t.Errorf("%s in a read-only directory succeeded", name)
		}
	}
	if _, err := mkdirScoped(merged, "/ro/sub/dir", copyOpts{}); err == nil {
		t.Error("mkdir in a read-only directory")
	}
}
