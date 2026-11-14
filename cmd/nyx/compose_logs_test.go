package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/logs"
)

func TestParseComposeLogsArgs(t *testing.T) {
	c, err := parseComposeLogsArgs(strings.Fields("-f -n 50 -t --no-prefix --no-color --file x.yaml --project=p api worker"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.follow || c.tail != 50 || !c.timestamps || !c.noPrefix || !c.noColor || c.file != "x.yaml" || c.project != "p" ||
		strings.Join(c.services, ",") != "api,worker" {
		t.Errorf("parsed %+v", c)
	}
	if c, _ := parseComposeLogsArgs(nil); c.tail != 200 || c.follow {
		t.Errorf("defaults %+v", c)
	}
	if c, _ := parseComposeLogsArgs([]string{"--tail=0", "--file=a", "--project", "q"}); c.tail != 0 || c.file != "a" || c.project != "q" {
		t.Errorf("= forms %+v", c)
	}
	for _, bad := range []string{"--tail", "--tail x", "--tail=-1", "--file", "--bogus", "-n"} {
		if _, err := parseComposeLogsArgs(strings.Fields(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestLogPrinter(t *testing.T) {
	ts := time.Date(2026, 11, 14, 10, 0, 0, 5, time.UTC)
	e := logs.Entry{Time: ts, Log: "hello\n"}

	var b bytes.Buffer
	p := newLogPrinter(&b, []string{"db", "worker"}, composeLogsCLI{}, false)
	p.print("db", e)
	p.print("worker", logs.Entry{Log: "no newline"})
	if want := "db     | hello\nworker | no newline\n"; b.String() != want {
		t.Errorf("plain:\n%q\nwant\n%q", b.String(), want)
	}

	b.Reset()
	p = newLogPrinter(&b, []string{"db"}, composeLogsCLI{noPrefix: true, timestamps: true}, false)
	p.print("db", e)
	if want := "2026-11-14T10:00:00.000000005Z hello\n"; b.String() != want {
		t.Errorf("timestamps: %q", b.String())
	}

	b.Reset()
	p = newLogPrinter(&b, []string{"a", "b"}, composeLogsCLI{}, true)
	p.print("b", e)
	if want := "\x1b[33mb |\x1b[0m hello\n"; b.String() != want {
		t.Errorf("color: %q", b.String())
	}
}

func TestMergeServiceLogs(t *testing.T) {
	t0 := time.Unix(100, 0)
	at := func(s int, msg string) logs.Entry {
		return logs.Entry{Time: t0.Add(time.Duration(s) * time.Second), Log: msg}
	}
	got := mergeServiceLogs([]string{"a", "b"}, [][]logs.Entry{
		{at(1, "a1"), at(3, "a3"), at(3, "a3b")},
		{at(2, "b2"), at(3, "b3")},
	})
	var order []string
	for _, l := range got {
		order = append(order, l.service+":"+l.entry.Log)
	}
	if want := "a:a1 b:b2 a:a3 a:a3b b:b3"; strings.Join(order, " ") != want {
		t.Errorf("order %v, want %s", order, want)
	}
}
