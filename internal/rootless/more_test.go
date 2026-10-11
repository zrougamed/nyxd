//go:build linux

package rootless

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

func TestSubIDParsing(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("# c\n\nbob:1:2\nme:x:5\nme:5:0\nme:7\nme:100000:65536\n"))
	if r, err := parseSubID(sc, map[string]bool{"me": true}, "f"); err != nil || r.start != 100000 || r.count != 65536 {
		t.Errorf("%+v %v", r, err)
	}
	if _, err := parseSubID(bufio.NewScanner(strings.NewReader("bob:1:2\n")), map[string]bool{"me": true}, "f"); err == nil {
		t.Error("no entry")
	}
	if _, err := lookupSubID("/nonexistent/subuid", 1000); err == nil {
		t.Error("missing file")
	}
	p := filepath.Join(t.TempDir(), "subuid")
	_ = os.WriteFile(p, []byte("12345:200000:65536\n"), 0o644)
	if r, err := lookupSubID(p, 12345); err != nil || r.start != 200000 {
		t.Errorf("by number: %+v %v", r, err)
	}
	if err := idmap("/bin/false", "1", 0, subRange{1, 1}); err == nil || !strings.Contains(err.Error(), "/bin/false") {
		t.Errorf("failing map tool: %v", err)
	}
	if firstErr(nil, nil) != nil || firstErr(nil, errors.New("x")) == nil {
		t.Error("firstErr")
	}
	if os.Geteuid() != 0 {
		if _, err := EnterRemap(100000, 65536, nil); err == nil || !strings.Contains(err.Error(), "needs root") {
			t.Errorf("remap as a user: %v", err)
		}
	}
}

func TestEnterRemapErrors(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	// Root of a single-ID namespace: argument and directory errors come first.
	if _, err := EnterRemap(0, 10, nil); err == nil || !strings.Contains(err.Error(), "invalid range") {
		t.Errorf("invalid range: %v", err)
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	if _, err := EnterRemap(100000, 10, []string{filepath.Join(f, "dir")}); err == nil {
		t.Error("dir under a file")
	}
	if _, err := EnterRemap(100000, 10, []string{t.TempDir()}); err == nil || !strings.Contains(err.Error(), "chown") {
		t.Errorf("chown to an unmapped uid: %v", err)
	}
	if Current().Rootless {
		t.Error("not inside nyxd's namespace")
	}
}
