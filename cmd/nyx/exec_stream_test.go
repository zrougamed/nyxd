package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestExecFailureFromTail(t *testing.T) {
	tail := []byte("hello\nnyxd: exec: crun exec: exit status 255\n")
	err := execFailureFromTail(tail)
	if err == nil || !strings.Contains(err.Error(), "crun exec") {
		t.Fatalf("got %v", err)
	}
	if execFailureFromTail([]byte("no marker")) != nil {
		t.Fatal("expected nil")
	}
}

func TestExecTailRing(t *testing.T) {
	r := &execTailRing{max: 32}
	if _, err := r.Write(bytes.Repeat([]byte("a"), 40)); err != nil {
		t.Fatal(err)
	}
	if len(r.Bytes()) > 32 {
		t.Fatalf("ring too long %d", len(r.Bytes()))
	}
}

func TestShellExit127Hint(t *testing.T) {
	err := errors.New("crun exec: exit status 127")
	tail := []byte("sh: netns: not found\n")
	out := shellExit127Hint(err, []string{"sh"}, tail, true)
	if !strings.Contains(out.Error(), "exit 0") {
		t.Fatalf("expected hint, got %v", out)
	}
	if shellExit127Hint(err, []string{"bash"}, tail, true) != err {
		t.Fatal("bash argv should not get sh hint")
	}
}

func TestMaybeExecShellHint_usesStreamTail(t *testing.T) {
	err := errors.New("nyxd: exec: crun exec: exit status 255")
	tail := []byte("executable file 'bash' not found in $PATH: No such file or directory\n")
	out := maybeExecShellHint(err, []string{"bash"}, "c1", tail)
	if !errors.Is(out, errExecReported) {
		t.Fatalf("expected errExecReported, got %v", out)
	}
}

func TestBashExecCleanFilter(t *testing.T) {
	var out bytes.Buffer
	f := newBashExecCleanFilter(&out)
	in := "2026-05-18T20:20:23.373367Z: 2026-05-18T20:20:23.373143Z: executable file `bash' not found in $PATH: No such file or directory\nnyxd: exec: crun exec: exit status 255\n"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "executable file `bash' not found in $PATH: No such file or directory"
	if strings.TrimSpace(out.String()) != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestNyxdExecTrailerFilter(t *testing.T) {
	var out bytes.Buffer
	f := newNyxdExecTrailerFilter(&out)
	in := "hello\nnyxd: exec: crun exec: exit status 1\n"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "hello" {
		t.Fatalf("got %q", out.String())
	}
}

func TestMaybeExecShellHint_noFalsePositiveOnGeneric255(t *testing.T) {
	err := errors.New("nyxd: exec: crun exec: exit status 255")
	out := maybeExecShellHint(err, []string{"bash"}, "c1", []byte("some unrelated output\n"))
	if out != err {
		t.Fatalf("expected unchanged error, got %v", out)
	}
}
