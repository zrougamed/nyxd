//go:build linux

package runtime

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/testutil/appbin"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

// exec against a real container (crun in a private user namespace).
func TestExecRealContainer(t *testing.T) {
	if _, err := exec.LookPath("crun"); err != nil {
		t.Skip("crun not installed")
	}
	if !nsrun.Run(t) {
		return
	}
	dir := t.TempDir()
	r, err := New("crun", dir+"/state")
	if err != nil {
		t.Fatal(err)
	}
	r.SetCgroupManager("disabled")
	ctx := context.Background()
	b := appbin.Bundle(t, dir, "c", "sleep", "60")
	done := make(chan error, 1)
	go func() { done <- r.RunForeground(ctx, "c", b, nil, nil, nil, false, nil) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, err := r.State(ctx, "c"); err == nil && s.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("container did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if l, err := r.List(ctx); err != nil || len(l) != 1 {
		t.Fatalf("list %v %v", l, err)
	}

	// TTY: output through the PTY, sizes applied, stdin forwarded.
	var out bytes.Buffer
	if err := r.Exec(ctx, "c", []string{"echo", "tty-ok"}, nil, &out, &out, true, &TTYSize{Rows: 40, Cols: 100}); err != nil || !strings.Contains(out.String(), "tty-ok") {
		t.Errorf("tty exec: %q %v", out.String(), err)
	}
	out.Reset()
	var errb bytes.Buffer
	// Typed input: the bytes must arrive after crun put the terminal in raw
	// mode, as with a person typing, or the host line discipline eats ^D.
	tin, tw := io.Pipe()
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = tw.Write([]byte("typed\n"))
		time.Sleep(200 * time.Millisecond)
		_, _ = tw.Write([]byte{4})
		time.Sleep(time.Second)
		tw.Close()
	}()
	if err := r.Exec(ctx, "c", []string{"cat"}, tin, &out, &errb, true, &TTYSize{}); err != nil || !strings.Contains(out.String(), "typed") {
		t.Errorf("tty exec with stdin: %q %v", out.String(), err)
	}
	if err := r.Exec(ctx, "c", []string{"false"}, nil, nil, nil, true, nil); err == nil {
		t.Error("tty exec failure")
	}
	// stdin that stays open (an HTTP body): returns when the command ends.
	pr, pw := io.Pipe()
	defer pw.Close()
	out.Reset()
	if err := r.Exec(ctx, "c", []string{"echo", "one-shot"}, pr, &out, nil, false, nil); err != nil || !strings.Contains(out.String(), "one-shot") {
		t.Errorf("exec with open stdin: %q %v", out.String(), err)
	}
	if err := r.Exec(ctx, "c", []string{"echo", "x"}, nil, nil, nil, true, nil); err != nil {
		t.Errorf("tty exec, discarded output: %v", err)
	}
	if err := r.Kill(ctx, "c", "KILL"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; ExitCode(err) != 137 {
		t.Errorf("exit after KILL: %v (%d)", err, ExitCode(err))
	}
	if err := r.Exec(ctx, "c", []string{"true"}, nil, nil, nil, true, nil); err == nil {
		t.Error("tty exec in a stopped container")
	}
}
