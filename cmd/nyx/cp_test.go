package main

import (
	"strings"
	"testing"
)

func TestCpSpec(t *testing.T) {
	for arg, want := range map[string][3]string{
		"web:/etc/hosts": {"web", "/etc/hosts", "true"},
		"web:relative":   {"web", "relative", "true"},
		"./a:b":          {"", "./a:b", "false"},
		"/abs/x:y":       {"", "/abs/x:y", "false"},
		"-":              {"", "-", "false"},
		"plain":          {"", "plain", "false"},
		":nocontainer":   {"", ":nocontainer", "false"},
	} {
		c, p, remote := cpSpec(arg)
		if c != want[0] || p != want[1] || (remote && want[2] != "true") || (!remote && want[2] != "false") {
			t.Errorf("%q: %q %q %v", arg, c, p, remote)
		}
	}
}

func TestCpArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a"}, "usage"},
		{[]string{"a", "b"}, "one of SRC and DEST"},
		{[]string{"x:/a", "y:/b"}, "between two containers"},
		{[]string{"--bogus", "x:/a", "."}, "unknown flag"},
	} {
		if err := doCp("/nonexistent.sock", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if err := doCp("/nonexistent.sock", []string{"--help"}); err != nil {
		t.Error(err)
	}
	if !strings.Contains(archiveURL("my web", "/a b", nil), "/v1/containers/my%20web/archive?path=%2Fa+b") {
		t.Error(archiveURL("my web", "/a b", nil))
	}
}
