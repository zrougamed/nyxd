package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// On a terminal the progress lines are redrawn in place.
func TestPullUITerminal(t *testing.T) {
	var out bytes.Buffer
	u := &pullUI{out: &out, tty: true, bars: map[string]*pullBar{}}
	u.ensureBar("sha256:aaaaaaaaaaaaaaaa", 1000, "layer")
	u.ensureBar("sha256:aaaaaaaaaaaaaaaa", 1000, "layer") // known: no new line
	u.ensureBar("sha256:bbbbbbbbbbbbbbbb", 0, "")
	u.progress("sha256:aaaaaaaaaaaaaaaa", 500) // throttled
	u.progress("sha256:unknown", 1)
	u.note("Pulling %s\n", "x:1")
	u.closeDigest("sha256:aaaaaaaaaaaaaaaa")
	u.closeDigest("sha256:aaaaaaaaaaaaaaaa") // already done
	u.closeDigest("sha256:bbbbbbbbbbbbbbbb")
	u.Close()
	s := out.String()
	for _, want := range []string{"\x1b[2A", "Pulling x:1", "[==============================]", "done"} {
		if !strings.Contains(s, want) {
			t.Errorf("terminal output lacks %q:\n%q", want, s)
		}
	}
	if len(u.order) != 2 || u.drawn != 2 {
		t.Errorf("bars %d, drawn %d", len(u.order), u.drawn)
	}
}

func TestPullBarLine(t *testing.T) {
	for _, c := range []struct {
		b    pullBar
		want string
	}{
		{pullBar{title: "t", cur: 2048}, "t  2"},
		{pullBar{title: "t", cur: 2048, done: true}, "t  done  2"},
		{pullBar{title: "t", size: 100, cur: 50}, "[===============>              ]"},
		{pullBar{title: "t", size: 100, cur: 500}, "[==============================]"},
	} {
		if got := c.b.line(); !strings.Contains(got, c.want) {
			t.Errorf("line(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestDaemonErrorMessages(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{500, `{"error":"disk full"}`, "disk full"},
		{500, `{"message":"try later"}`, "try later"},
		{500, `{"other":1}`, `{"other":1}`},
		{404, "", "not found (is nyxd older"},
		{503, "", "service unavailable"},
		{502, " ", "nyxd: bad gateway"},
	} {
		if got := daemonError(c.status, []byte(c.body)).Error(); !strings.Contains(got, c.want) {
			t.Errorf("daemonError(%d, %q) = %q, want %q", c.status, c.body, got, c.want)
		}
	}
	if httpStatusNotFound(nil) || !httpStatusNotFound(errors.New("HTTP 404: not found")) {
		t.Error("httpStatusNotFound")
	}
}

func TestPullResponses(t *testing.T) {
	for _, c := range []struct {
		name, body string
		args       []string
		want       string // in stdout
		wantErr    string
	}{
		{"json no newline", `{"ok":true}`, []string{"pull", "--json", "x"}, "{\"ok\":true}\n", ""},
		{"plain body", "pulled x\n", []string{"pull", "x"}, "pulled x", ""},
		{"stream error no message", "{\"phase\":\"error\"}\n{\"phase\":\"x\"}\n", []string{"pull", "x"}, "", "pull failed"},
		{"stream too long", "{\"phase\":\"" + strings.Repeat("a", 20<<20) + "\"}\n\n", []string{"pull", "x"}, "", "pull stream"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sock := stub(t, reply(200, c.body))
			out, errOut, err := nyx(t, append([]string{"-socket", sock}, c.args...)...)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("err %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(out, c.want) {
				t.Errorf("err %v, out %q (stderr %q), want %q", err, out, errOut, c.want)
			}
		})
	}
}
