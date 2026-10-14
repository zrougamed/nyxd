package rootfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUserFileQuirks(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)                                                                                                                         //nolint:errcheck
	os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte("# users\n\nroot:x:0:0::/root:/bin/sh\nbad:x:notanumber:1::/:/bin/sh\napp:x:1000:1000::/:/bin/sh\n"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(root, "etc", "group"), []byte("short:x\nwheel:x:10:app\nother:x:20:root,app\n"), 0o644)                                                     //nolint:errcheck
	if _, err := ResolveUser(root, "bad"); err == nil || !strings.Contains(err.Error(), "unable to find user bad") {
		t.Errorf("non-numeric uid: %v", err)
	}
	u, err := ResolveUser(root, "app")
	if err != nil || u.UID != 1000 || len(u.AdditionalGids) != 2 {
		t.Errorf("app: %+v %v", u, err)
	}
	// No passwd or group file: numeric ids still work.
	if u, err := ResolveUser(t.TempDir(), "5:6"); err != nil || u.UID != 5 || u.GID != 6 {
		t.Errorf("numeric without files: %+v %v", u, err)
	}
}
