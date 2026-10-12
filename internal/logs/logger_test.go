package logs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestCollector(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewCollector(filepath.Join(dir, "f", "x"), quiet); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "file"), nil, 0o600)
	if _, err := NewCollector(filepath.Join(dir, "file", "logs"), quiet); err == nil {
		t.Error("log dir under a file")
	}
	c, err := NewCollector(filepath.Join(dir, "logs"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	c.Stream(context.Background(), "c1", "stdout", strings.NewReader("one\ntwo\nthree\n"))
	c.Stream(context.Background(), "c1", "stderr", strings.NewReader("err\n"+strings.Repeat("x", 70*1024)+"\n"))
	all, err := c.Tail("c1", 10)
	if err != nil || len(all) != 4 || all[3].Stream != "stderr" {
		t.Fatalf("tail all: %+v %v", all, err)
	}
	last, _ := c.Tail("c1", 2)
	if len(last) != 2 || last[1].Log != "err" {
		t.Errorf("tail 2: %+v", last)
	}
	if one, _ := c.Tail("c1", 1); len(one) != 1 {
		t.Errorf("tail 1 (trimming while reading): %+v", one)
	}
	// A corrupt line ends the read.
	f, _ := os.OpenFile(filepath.Join(dir, "logs", "c1.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{broken\n") //nolint:errcheck
	f.Close()
	if got, _ := c.Tail("c1", 1); len(got) != 1 {
		t.Errorf("tail after corrupt line: %+v", got)
	}
	if _, err := c.Tail("missing", 1); err == nil {
		t.Error("tail of a missing log")
	}
	if err := c.Rotate("c1"); err != nil {
		t.Error(err)
	}
	c.Remove("c1")
	if _, err := os.Stat(filepath.Join(dir, "logs", "c1.log")); !os.IsNotExist(err) {
		t.Error("log not removed")
	}
	// A cancelled stream stops; an unopenable log file is reported.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	go func() { time.Sleep(50 * time.Millisecond); pw.Write([]byte("late\n")); pw.Close() }() //nolint:errcheck
	c.Stream(ctx, "c2", "stdout", pr)
	_ = os.Mkdir(filepath.Join(dir, "logs", "c3.log"), 0o700)
	c.Stream(context.Background(), "c3", "stdout", strings.NewReader("x\n"))
	// Writes into a log whose directory is gone are logged.
	c2, _ := NewCollector(filepath.Join(dir, "gone"), quiet)
	pr2, pw2 := io.Pipe()
	done := make(chan struct{})
	go func() { c2.Stream(context.Background(), "c4", "stdout", pr2); close(done) }()
	pw2.Write([]byte("a\n")) //nolint:errcheck
	pw2.Close()
	<-done
}
