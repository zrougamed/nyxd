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
