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

func TestParseRunArgsVolumesNetwork(t *testing.T) {
	o, err := parseRunArgs(strings.Fields("-v /a:/a -v=/b:/b:ro --volume /c:/c --volume=/d:/d --network host alpine true"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.volumes, " ") != "/a:/a /b:/b:ro /c:/c /d:/d" || o.network != "host" || o.image != "alpine" {
		t.Fatalf("got volumes=%q network=%q image=%q", o.volumes, o.network, o.image)
	}
	for _, args := range []string{"--net=host alpine", "--network=host alpine", "--net host alpine"} {
		if o, err := parseRunArgs(strings.Fields(args)); err != nil || o.network != "host" {
			t.Errorf("%s: network=%q err=%v", args, o.network, err)
		}
	}
	for _, args := range []string{"-v", "--volume", "--network", "--net"} {
		if _, err := parseRunArgs([]string{args}); err == nil || !strings.Contains(err.Error(), "requires a value") {
			t.Errorf("%s without value: %v", args, err)
		}
	}
}

func TestParseRunArgsLimits(t *testing.T) {
	o, err := parseRunArgs(strings.Fields("-m 256m --memory-swap=512m --cpus 0.5 --pids-limit 200 --oom-score-adj=-100 --read-only --tmpfs /tmp:size=64m --tmpfs=/run alpine"))
	if err != nil {
		t.Fatal(err)
	}
	l := o.limits
	if l.Memory != "256m" || l.MemorySwap != "512m" || l.CPUs != "0.5" || l.PidsLimit != 200 || l.OomScoreAdj == nil || *l.OomScoreAdj != -100 {
		t.Errorf("limits = %+v", l)
	}
	if !o.readOnly || strings.Join(o.tmpfs, "|") != "/tmp:size=64m|/run" || o.image != "alpine" {
		t.Errorf("opts = %+v", o)
	}

	body := map[string]any{}
	addRunLimitFields(body, o)
	for _, k := range []string{"memory", "memory_swap", "cpus", "pids_limit", "oom_score_adj", "read_only", "tmpfs"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body lacks %s: %v", k, body)
		}
	}
	empty := map[string]any{}
	addRunLimitFields(empty, runOpts{})
	if len(empty) != 0 {
		t.Errorf("no flags still set %v", empty)
	}

	for _, bad := range []string{"--memory", "--pids-limit x alpine", "--oom-score-adj= alpine", "--tmpfs"} {
		if _, err := parseRunArgs(strings.Fields(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
