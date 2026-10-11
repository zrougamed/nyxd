//go:build linux

package layer

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestOverlayMountSemantics mounts real overlayfs on layers produced by Apply.
// It needs mount privileges, so it only runs when NYXD_OVERLAY_IT=1, e.g.:
//
//	unshare -Urm env NYXD_OVERLAY_IT=1 go test ./internal/layer -run OverlayMount
//
// Inside an unprivileged user namespace the mount uses "userxattr".
func TestOverlayMountSemantics(t *testing.T) {
	if os.Getenv("NYXD_OVERLAY_IT") != "1" {
		t.Skip("set NYXD_OVERLAY_IT=1 and run with mount privileges")
	}
	base := t.TempDir()
	l1, l2 := filepath.Join(base, "l1"), filepath.Join(base, "l2")
	upper, work, merged := filepath.Join(base, "u"), filepath.Join(base, "w"), filepath.Join(base, "m")
	for _, d := range []string{l1, l2, upper, work, merged} {
		os.Mkdir(d, 0o755)
	}
	opts := ApplyOptions{Mode: ModeOverlay, XattrPrefix: "user."}
	if _, err := Apply(mkTar(t, true,
		ent{name: "etc/keep", typ: tar.TypeReg, body: "k"},
		ent{name: "etc/gone", typ: tar.TypeReg, body: "g"},
		ent{name: "var/cache/old", typ: tar.TypeReg, body: "o"},
	), l1, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(mkTar(t, true,
		ent{name: "etc/.wh.gone", typ: tar.TypeReg},
		ent{name: "var/cache/.wh..wh..opq", typ: tar.TypeReg},
		ent{name: "var/cache/new", typ: tar.TypeReg, body: "n"},
	), l2, opts); err != nil {
		t.Fatal(err)
	}
	mopts := "userxattr,lowerdir=" + l2 + ":" + l1 + ",upperdir=" + upper + ",workdir=" + work
	if err := syscall.Mount("overlay", merged, "overlay", 0, mopts); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer syscall.Unmount(merged, syscall.MNT_DETACH)

	if got := tree(t, merged); strings.Join(got, ",") != "etc,etc/keep,var,var/cache,var/cache/new" {
		t.Fatalf("merged tree = %v", got)
	}

	// Container-side changes, then Diff the upper dir back into a layer.
	os.Remove(filepath.Join(merged, "etc/keep"))
	os.WriteFile(filepath.Join(merged, "etc/added"), []byte("a"), 0o644)
	os.RemoveAll(filepath.Join(merged, "var/cache"))
	os.MkdirAll(filepath.Join(merged, "var/cache"), 0o755)
	os.WriteFile(filepath.Join(merged, "var/cache/fresh"), []byte("f"), 0o644)

	var names []string
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(Diff(upper, pw, DiffOptions{XattrPrefix: "user."})) }()
	tr := tar.NewReader(pr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	want := "etc/,etc/added,etc/.wh.keep,var/,var/cache/,var/cache/.wh..wh..opq,var/cache/fresh"
	if strings.Join(names, ",") != want {
		t.Fatalf("diff = %v\nwant   %s", names, want)
	}
}
