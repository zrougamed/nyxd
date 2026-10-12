//go:build linux

package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCrun is a crun stand-in; FAKE_CRUN selects its answers.
const fakeCrun = `#!/bin/sh
for a; do case $a in state|list|kill|delete|exec|run) cmd=$a; break ;; esac; done
case "$FAKE_CRUN:$cmd" in
badjson:state | badjson:list) echo '{not json' ;;
stopped:state) echo '{"id":"c","status":"stopped","exit_code":7}' ;;
stoppedraw:state) echo '{"id":"c","status":"stopped","exitStatus":9}' ;;
stoppednone:state) echo '{"id":"c","status":"stopped"}' ;;
running:state) echo '{"id":"c","status":"running"}' ;;
absent:*) echo "error opening file /x/c/status: No such file or directory" >&2; exit 1 ;;
fail:*) echo "boom" >&2; exit 1 ;;
*:list) echo '[{"id":"a","status":"running"}]' ;;
*:exec) echo out; echo err >&2 ;;
*:run) echo "run failed" >&2; exit 3 ;;
esac
`

func fakeRuntime(t *testing.T, mode string) *Runtime {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "crun")
	if err := os.WriteFile(bin, []byte(fakeCrun), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CRUN", mode)
	r, err := New(bin, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	r.SetCgroupManager("disabled")
	return r
}

func TestNew(t *testing.T) {
	if _, err := New("no-such-crun-binary", t.TempDir()); err == nil {
		t.Error("missing binary")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New("/bin/sh", filepath.Join(dir, "f", "state")); err == nil {
		t.Error("state dir under a file")
	}
	if err := os.WriteFile(filepath.Join(dir, "state-pids"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New("/bin/sh", filepath.Join(dir, "state")); err == nil {
		t.Error("pid dir is a file")
	}
	r, err := New("sh", filepath.Join(dir, "ok"))
	if err != nil || !strings.HasSuffix(r.Binary(), "/sh") || r.RootDir() != filepath.Join(dir, "ok") {
		t.Fatalf("PATH lookup: %v %+v", err, r)
	}
	r.SetCgroupManager("")
	if len(r.GlobalArgs()) != 0 {
		t.Error("empty cgroup manager must clear globals")
	}
}

func TestStateListKillDelete(t *testing.T) {
	ctx := context.Background()
	r := fakeRuntime(t, "running")
	if s, err := r.State(ctx, "c"); err != nil || s.Status != "running" {
		t.Fatalf("state: %+v %v", s, err)
	}
	if l, err := r.List(ctx); err != nil || len(l) != 1 {
		t.Fatalf("list: %v %v", l, err)
	}
	if err := r.Kill(ctx, "c", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "c", true); err != nil {
		t.Fatal(err)
	}

	r = fakeRuntime(t, "badjson")
	if _, err := r.State(ctx, "c"); err == nil {
		t.Error("state parse error")
	}
	if _, err := r.List(ctx); err == nil {
		t.Error("list parse error")
	}
	r = fakeRuntime(t, "fail")
	if _, err := r.List(ctx); err == nil {
		t.Error("list failure")
	}
	if err := r.Delete(ctx, "c", false); err == nil || !strings.Contains(err.Error(), "crun delete") {
		t.Errorf("delete failure names the subcommand: %v", err)
	}
	r = fakeRuntime(t, "absent")
	if err := r.Delete(ctx, "c", false); err != nil {
		t.Errorf("delete of an absent container: %v", err)
	}
}

func TestWaits(t *testing.T) {
	ctx := context.Background()
	for mode, want := range map[string]int{"stopped": 7, "stoppedraw": 9, "stoppednone": -1, "absent": -1} {
		r := fakeRuntime(t, mode)
		if code, err := r.WaitForExit(ctx, "c"); err != nil || code != want {
			t.Errorf("%s: WaitForExit = %d, %v; want %d", mode, code, err, want)
		}
		if err := r.WaitStopped(ctx, "c"); err != nil {
			t.Errorf("%s: WaitStopped: %v", mode, err)
		}
	}
	// Still running or failing: only the context ends the wait.
	for _, mode := range []string{"running", "fail"} {
		r := fakeRuntime(t, mode)
		cctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		if _, err := r.WaitForExit(cctx, "c"); err == nil {
			t.Errorf("%s: WaitForExit returned", mode)
		}
		if err := r.WaitStopped(cctx, "c"); err == nil {
			t.Errorf("%s: WaitStopped returned", mode)
		}
		cancel()
	}

	// Exit code from the state file crun leaves.
	r := fakeRuntime(t, "stoppednone")
	if err := os.MkdirAll(filepath.Join(r.RootDir(), "c"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.RootDir(), "c", "exit_code"), []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := r.resolveExitCode(ctx, "c", nil); code != 42 {
		t.Errorf("exit_code file: %d", code)
	}
	if err := os.WriteFile(filepath.Join(r.RootDir(), "c", "exit_code"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := r.resolveExitCode(ctx, "c", nil); code != -1 {
		t.Errorf("bad exit_code file: %d", code)
	}
	r = fakeRuntime(t, "badjson")
	if code := r.resolveExitCode(ctx, "c", nil); code != -1 {
		t.Errorf("raw state not json: %d", code)
	}
}

func TestExecWriters(t *testing.T) {
	ctx := context.Background()
	r := fakeRuntime(t, "running")
	if err := r.Exec(ctx, "c", nil, nil, nil, nil, false, nil); err == nil {
		t.Error("empty argv")
	}
	var out, errb bytes.Buffer
	if err := r.Exec(ctx, "c", []string{"x"}, nil, &out, &errb, false, nil); err != nil || out.String() != "out\n" || errb.String() != "err\n" {
		t.Errorf("split: %q %q %v", out.String(), errb.String(), err)
	}
	out.Reset()
	if err := r.Exec(ctx, "c", []string{"x"}, strings.NewReader("in"), &out, nil, false, nil); err != nil || out.String() != "out\nerr\n" {
		t.Errorf("merged: %q %v", out.String(), err)
	}
	errb.Reset()
	if err := r.Exec(ctx, "c", []string{"x"}, nil, nil, &errb, false, nil); err != nil || errb.String() != "err\n" {
		t.Errorf("stderr only: %q %v", errb.String(), err)
	}
	if err := r.Exec(ctx, "c", []string{"x"}, nil, nil, nil, false, nil); err != nil {
		t.Errorf("discard: %v", err)
	}
	if err := fakeRuntime(t, "fail").Exec(ctx, "c", []string{"x"}, nil, nil, nil, false, nil); err == nil || !strings.Contains(err.Error(), "crun exec") {
		t.Errorf("exec failure: %v", err)
	}
	if err := fakeRuntime(t, "running").RunForeground(ctx, "c", t.TempDir(), nil, nil); err == nil || !strings.Contains(err.Error(), "run failed") {
		t.Errorf("run failure carries stderr: %v", err)
	}
}

func TestIsCrunAbsent(t *testing.T) {
	for msg, want := range map[string]bool{
		"container `x` not found": true, "cannot find": true, "could not find": true, "does not exist": true,
		"no such container": true, "unable to find": true, "open /r/x/status: No such file or directory": true,
		"open /etc/x: No such file or directory": false, "permission denied": false,
	} {
		if got := CrunContainerAbsent(errorString(msg)); got != want {
			t.Errorf("%q: %v", msg, got)
		}
	}
	if CrunContainerAbsent(nil) {
		t.Error("nil")
	}
	if subcommand([]string{"--cgroup-manager=disabled", "--root", "/r", "state", "x"}) != "state" || subcommand([]string{"-x"}) != "?" {
		t.Error("subcommand")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
