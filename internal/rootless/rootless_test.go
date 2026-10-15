//go:build linux

package rootless

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSubID(t *testing.T) {
	data := "# comment\nalice:100000:65536\nbob:165536:65536\n1001:231072:1000\nbad:line\n"
	cases := []struct {
		keys      map[string]bool
		start, n  int
		wantError bool
	}{
		{map[string]bool{"bob": true}, 165536, 65536, false},
		{map[string]bool{"1001": true, "carol": true}, 231072, 1000, false},
		{map[string]bool{"dave": true}, 0, 0, true},
	}
	for _, c := range cases {
		r, err := parseSubID(bufio.NewScanner(strings.NewReader(data)), c.keys, "subuid")
		if c.wantError != (err != nil) || r.start != c.start || r.count != c.n {
			t.Errorf("keys %v: got %+v, %v", c.keys, r, err)
		}
	}
}

func TestSetEnv(t *testing.T) {
	env := setEnv([]string{"A=1", "_NYXD_USERNS=map", "B=2"}, envStage, stageReady)
	if strings.Join(env, ",") != "A=1,B=2,_NYXD_USERNS=ready" {
		t.Fatalf("env = %v", env)
	}
}

func TestCleanEnv(t *testing.T) {
	env := CleanEnv([]string{"A=1", "_NYXD_USERNS=ready", "_NYXD_USERNS_MAPPING=subid", "_NYXD_HOST_UID=1000", "B=2"})
	if strings.Join(env, ",") != "A=1,B=2" {
		t.Fatalf("env = %v", env)
	}
}

func TestPreviousNamespace(t *testing.T) {
	proc, pids := t.TempDir(), t.TempDir()
	mkproc := func(pid, userns string, uid int) {
		d := filepath.Join(proc, pid, "ns")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(userns, filepath.Join(d, "user")); err != nil {
			t.Fatal(err)
		}
		status := fmt.Sprintf("Name:\tsh\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
		if err := os.WriteFile(filepath.Join(proc, pid, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkproc("self", "user:[1]", 1000)
	pidfile := func(name, pid string) {
		if err := os.WriteFile(filepath.Join(pids, name+".pid"), []byte(pid+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := previousNamespace(proc, pids, 1000); got != 0 {
		t.Fatalf("no pid files: got %d", got)
	}
	mkproc("10", "user:[1]", 1000) // our own namespace
	mkproc("11", "user:[2]", 2000) // someone else's process
	pidfile("same", "10")
	pidfile("other-user", "11")
	pidfile("gone", "12")
	pidfile("junk", "x")
	if got := previousNamespace(proc, pids, 1000); got != 0 {
		t.Fatalf("no earlier namespace: got %d", got)
	}
	mkproc("13", "user:[3]", 1000)
	pidfile("earlier", "13")
	if got := previousNamespace(proc, pids, 1000); got != 13 {
		t.Fatalf("got %d, want 13", got)
	}
}
