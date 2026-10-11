package rootfs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScopedJoin(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "run"), 0o755)
	os.MkdirAll(filepath.Join(root, "usr/lib"), 0o755)
	os.MkdirAll(filepath.Join(root, "var"), 0o755)
	os.Symlink("/run", filepath.Join(root, "var/run"))           // absolute, re-rooted
	os.Symlink("usr/lib", filepath.Join(root, "lib"))            // relative
	os.Symlink("../../../../etc", filepath.Join(root, "escape")) // tries to leave root
	os.Symlink("loop", filepath.Join(root, "loop"))

	cases := map[string]string{
		"/var/run/x":       "run/x",
		"lib/libc.so":      "usr/lib/libc.so",
		"/escape/passwd":   "etc/passwd",
		"/../../etc/x":     "etc/x",
		"/new/dir/file":    "new/dir/file",
		"/usr/lib/../bin/": "usr/bin",
	}
	for in, want := range cases {
		got, err := ScopedJoin(root, in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != filepath.Join(root, want) {
			t.Errorf("%s = %s, want %s", in, got, filepath.Join(root, want))
		}
	}
	if _, err := ScopedJoin(root, "/loop/x"); err == nil {
		t.Fatal("symlink loop not detected")
	}
}

func TestResolveUser(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/passwd"), []byte("root:x:0:0::/root:/bin/sh\nnginx:x:101:101::/:/sbin/nologin\napp:x:1000:1000::/home/app:/bin/sh\n"), 0o644)
	os.WriteFile(filepath.Join(root, "etc/group"), []byte("root:x:0:\nnginx:x:101:\napp:x:1000:\nwww:x:33:app,nginx\n"), 0o644)

	cases := []struct {
		spec     string
		uid, gid uint32
		extra    []uint32
		wantErr  bool
	}{
		{"", 0, 0, nil, false},
		{"app", 1000, 1000, []uint32{33}, false},
		{"1000", 1000, 1000, []uint32{33}, false},
		{"nginx:www", 101, 33, nil, false},
		{"4242", 4242, 0, nil, false},
		{"5:7", 5, 7, nil, false},
		{"ghost", 0, 0, nil, true},
		{"app:ghosts", 0, 0, nil, true},
	}
	for _, c := range cases {
		u, err := ResolveUser(root, c.spec)
		if c.wantErr != (err != nil) {
			t.Fatalf("%q: err = %v", c.spec, err)
		}
		if err == nil && (u.UID != c.uid || u.GID != c.gid || !reflect.DeepEqual(u.AdditionalGids, c.extra)) {
			t.Errorf("%q = %+v", c.spec, u)
		}
	}
}
