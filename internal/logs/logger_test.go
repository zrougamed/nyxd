package logs

import (
	"context"
	"fmt"
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
	if err != nil || len(all) != 6 || all[3].Stream != "stderr" || len(all[4].Log) != maxLine || len(all[5].Log) != 70*1024-maxLine {
		t.Fatalf("tail all: %+v %v", all, err)
	}
	last, _ := c.Tail("c1", 2)
	if len(last) != 2 || last[0].Log != strings.Repeat("x", maxLine) {
		t.Errorf("tail 2: %+v", last)
	}
	if one, _ := c.Tail("c1", 1); len(one) != 1 {
		t.Errorf("tail 1 (trimming while reading): %+v", one)
	}
	// A corrupt line is skipped.
	f, _ := os.OpenFile(filepath.Join(dir, "logs", "c1.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{broken\n") //nolint:errcheck
	f.Close()
	if got, _ := c.Tail("c1", 1); len(got) != 1 {
		t.Errorf("tail after corrupt line: %+v", got)
	}
	if _, err := c.Tail("missing", 1); err == nil {
		t.Error("tail of a missing log")
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

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCollector(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRotation(1024, 3)
	var in strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&in, "line %03d\n", i)
	}
	// stdout and stderr share one file and rotate it together.
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() { c.Stream(context.Background(), "r", "stderr", pr); close(done) }()
	c.Stream(context.Background(), "r", "stdout", strings.NewReader(in.String()))
	pw.Write([]byte("last\n")) //nolint:errcheck
	pw.Close()
	<-done

	names, _ := filepath.Glob(filepath.Join(dir, "r.log*"))
	if len(names) != 3 {
		t.Fatalf("files: %v", names)
	}
	for _, n := range names {
		if st, _ := os.Stat(n); st.Size() > 1024 {
			t.Errorf("%s: %d bytes", n, st.Size())
		}
	}
	// Tail reads across the rotated files, oldest first.
	got, err := c.Tail("r", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 20 || got[len(got)-1].Log != "last" || got[len(got)-2].Log != "line 099" {
		t.Fatalf("tail: %d entries, last %+v", len(got), got[len(got)-1])
	}
	for i := 1; i < len(got)-1; i++ {
		if got[i].Log <= got[i-1].Log {
			t.Fatalf("out of order at %d: %q after %q", i, got[i].Log, got[i-1].Log)
		}
	}
	if few, _ := c.Tail("r", 3); len(few) != 3 || few[0].Log != "line 098" {
		t.Errorf("tail 3: %+v", few)
	}
	c.Remove("r")
	if names, _ := filepath.Glob(filepath.Join(dir, "r.log*")); len(names) != 0 {
		t.Errorf("left after remove: %v", names)
	}

	// One file: rotation starts it over.
	c.SetRotation(256, 0)
	c.Stream(context.Background(), "one", "stdout", strings.NewReader(in.String()))
	names, _ = filepath.Glob(filepath.Join(dir, "one.log*"))
	if len(names) != 1 {
		t.Fatalf("files: %v", names)
	}
	if got, _ := c.Tail("one", 1); len(got) != 1 || got[0].Log != "line 099" {
		t.Errorf("tail: %+v", got)
	}

	// No rotation.
	c.SetRotation(0, 3)
	c.Stream(context.Background(), "big", "stdout", strings.NewReader(in.String()))
	if got, _ := c.Tail("big", 1000); len(got) != 100 {
		t.Errorf("unrotated: %d entries", len(got))
	}
}

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 5000; i++ { // several 32 KiB blocks
		fmt.Fprintf(&b, "%05d %s\n", i, strings.Repeat("y", 20))
	}
	if err := os.WriteFile(Path(dir, "t"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, end, err := TailLines(dir, "t", 4000)
	if err != nil || len(lines) != 4000 || end != int64(b.Len()) {
		t.Fatalf("%d lines, end %d, %v", len(lines), end, err)
	}
	if !strings.HasPrefix(string(lines[0]), "01000 ") || !strings.HasPrefix(string(lines[3999]), "04999 ") {
		t.Errorf("range %q … %q", lines[0][:5], lines[3999][:5])
	}
	if lines, _, _ := TailLines(dir, "t", 0); len(lines) != 0 {
		t.Errorf("n=0: %d", len(lines))
	}
	os.WriteFile(Path(dir, "e"), nil, 0o600) //nolint:errcheck
	if lines, end, err := TailLines(dir, "e", 5); len(lines) != 0 || end != 0 || err != nil {
		t.Errorf("empty: %v %d %v", lines, end, err)
	}
	os.Mkdir(Path(dir, "d"), 0o700) //nolint:errcheck
	if _, _, err := TailLines(dir, "d", 5); err == nil {
		t.Error("directory as log")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "100": 100, "512k": 512 << 10, "10m": 10 << 20, "10MB": 10 << 20, "1g": 1 << 30, " 2M ": 2 << 20} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "x", "-1", "1t", "m"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
