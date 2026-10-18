package main

import (
	"strings"
	"testing"
)

func TestParseRunArgsCommand(t *testing.T) {
	cases := []struct {
		args       string
		image, cmd string
	}{
		{"alpine", "alpine", ""},
		{"alpine sleep 300", "alpine", "sleep|300"},
		{"alpine -- sleep 300", "alpine", "sleep|300"},
		{"-d --name x alpine sh -c echo", "alpine", "sh|-c|echo"},
		{"alpine ls -- -la", "alpine", "ls|--|-la"},
		{"-e A=1 alpine:3.20 -- --version", "alpine:3.20", "--version"},
	}
	for _, c := range cases {
		o, err := parseRunArgs(strings.Fields(c.args))
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if o.image != c.image || strings.Join(o.cmdArgs, "|") != c.cmd {
			t.Errorf("%q: image=%q cmd=%q, want %q %q", c.args, o.image, strings.Join(o.cmdArgs, "|"), c.image, c.cmd)
		}
	}
	for _, bad := range []string{"", "--", "-d", "--bogus alpine"} {
		if _, err := parseRunArgs(strings.Fields(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestParseRunArgsDockerFlags(t *testing.T) {
	o, err := parseRunArgs(strings.Fields("--rm -it --name sh alpine:3.20"))
	if err != nil {
		t.Fatal(err)
	}
	if !o.rm || !o.stdin || !o.tty || o.name != "sh" || o.image != "alpine:3.20" {
		t.Fatalf("got rm=%v stdin=%v tty=%v name=%q image=%q", o.rm, o.stdin, o.tty, o.name, o.image)
	}
	o, err = parseRunArgs(strings.Fields("-d -i -t alpine"))
	if err != nil {
		t.Fatal(err)
	}
	if !o.detach || !o.stdin || !o.tty {
		t.Fatalf("detach/i/t: %+v", o)
	}
	o, err = parseRunArgs(strings.Fields("-ti alpine sh"))
	if err != nil || !o.stdin || !o.tty || strings.Join(o.cmdArgs, " ") != "sh" {
		t.Fatalf("-ti: %+v err=%v", o, err)
	}
}
