//go:build linux

package layer

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

// Every kind of entry reports a destination it cannot write (a read-only
// directory, for a non-root user) instead of skipping it.
func TestApplyReportsWriteErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	setup := func(t *testing.T) string {
		dest := t.TempDir()
		ro := filepath.Join(dest, "ro")
		for _, d := range []string{"ro/old/sub", "ro/olddir"} {
			os.MkdirAll(filepath.Join(dest, d), 0o755) //nolint:errcheck
		}
		os.WriteFile(filepath.Join(ro, "f"), []byte("x"), 0o644)        //nolint:errcheck
		os.WriteFile(filepath.Join(ro, "notdir"), []byte("x"), 0o644)   //nolint:errcheck
		os.WriteFile(filepath.Join(dest, "target"), []byte("x"), 0o644) //nolint:errcheck
		os.Chmod(ro, 0o555)                                             //nolint:errcheck
		t.Cleanup(func() { os.Chmod(ro, 0o755) })                       //nolint:errcheck
		return dest
	}
	for _, c := range []struct {
		name string
		mode Mode
		ents []ent
	}{
		{"file", ModeFlatten, []ent{{name: "ro/new", body: "x", typ: tar.TypeReg}}},
		{"replace file", ModeFlatten, []ent{{name: "ro/f", body: "y", typ: tar.TypeReg}}},
		{"directory", ModeFlatten, []ent{{name: "ro/newdir/", typ: tar.TypeDir}}},
		{"file over directory", ModeFlatten, []ent{{name: "ro/olddir", typ: tar.TypeDir}, {name: "ro/olddir/x", body: "1", typ: tar.TypeReg}, {name: "ro/old", body: "x", typ: tar.TypeReg}}},
		{"directory over file", ModeFlatten, []ent{{name: "ro/f/", typ: tar.TypeDir}}},
		{"symlink", ModeFlatten, []ent{{name: "ro/l", link: "x", typ: tar.TypeSymlink}}},
		{"replace symlink", ModeFlatten, []ent{{name: "ro/f", link: "x", typ: tar.TypeSymlink}}},
		{"hardlink", ModeFlatten, []ent{{name: "ro/h", link: "target", typ: tar.TypeLink}}},
		{"replace hardlink", ModeFlatten, []ent{{name: "ro/f", link: "target", typ: tar.TypeLink}}},
		{"fifo", ModeFlatten, []ent{{name: "ro/p", typ: tar.TypeFifo}}},
		{"replace fifo", ModeFlatten, []ent{{name: "ro/f", typ: tar.TypeFifo}}},
		{"parent", ModeFlatten, []ent{{name: "ro/a/b", body: "x", typ: tar.TypeReg}}},
		{"parent is a file", ModeFlatten, []ent{{name: "ro/notdir/b", body: "x", typ: tar.TypeReg}}},
		{"whiteout parent", ModeOverlay, []ent{{name: "ro/a/.wh.x", typ: tar.TypeReg}}},
		{"whiteout", ModeFlatten, []ent{{name: "ro/.wh.f", typ: tar.TypeReg}}},
		{"overlay whiteout", ModeOverlay, []ent{{name: "ro/.wh.gone", typ: tar.TypeReg}}},
		{"opaque", ModeFlatten, []ent{{name: "ro/.wh..wh..opq", typ: tar.TypeReg}}},
		{"nested opaque", ModeFlatten, []ent{{name: "w/.wh..wh..opq", typ: tar.TypeReg}, {name: "w/old/", typ: tar.TypeDir}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dest := setup(t)
			// The nested opaque case keeps w/old (this layer declares it) and
			// fails to prune w/old/sub, which cannot be emptied.
			if c.name == "nested opaque" {
				sub := filepath.Join(dest, "w", "old", "sub")
				os.MkdirAll(sub, 0o755)                           //nolint:errcheck
				os.WriteFile(filepath.Join(sub, "f"), nil, 0o644) //nolint:errcheck
				os.Chmod(sub, 0o555)                              //nolint:errcheck
				t.Cleanup(func() { os.Chmod(sub, 0o755) })        //nolint:errcheck
			}
			if _, err := Apply(mkTar(t, false, c.ents...), dest, ApplyOptions{Mode: c.mode, XattrPrefix: "user."}); err == nil {
				t.Error("write into a read-only directory succeeded")
			}
		})
	}
}

// Chown to an id this user does not own fails without privileges.
func TestApplyChownError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root may chown")
	}
	ents := []ent{{name: "f", body: "x", typ: tar.TypeReg}}
	opts := ApplyOptions{Mode: ModeFlatten, Chown: true, IDMap: func(int, int) (int, int) { return 12345, 12345 }}
	if _, err := Apply(mkTar(t, false, ents...), t.TempDir(), opts); err == nil {
		t.Error("chown to another user succeeded")
	}
}
