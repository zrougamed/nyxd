package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Argument errors are reported before anything reaches the daemon.
func TestArgumentErrors(t *testing.T) {
	sock := stub(t, reply(200, `{"ok":true,"id":"x","removed":["a"],"stopped":[],"ids":[]}`))
	for _, args := range [][]string{
		{"run", "-p"}, {"run", "-e"}, {"run", "--name"}, {"run", "--hostname"}, {"run", "-h"}, {"run", "--restart"},
		{"run", "--bogus", "x"}, {"run"}, {"run", "--", "cmd"},
		{"rm", "-f", "x"}, {"container", "logs"}, {"logs", "--tail=x", "c"}, {"logs", "--bogus", "c"},
		{"image", "ls", "extra"}, {"image", "rm", "--force"}, {"image", "prune", "extra"},
		{"push", "-u"}, {"push", "--password"}, {"push", "--bogus", "a"}, {"push", "a", "b"},
		{"save", "-o"}, {"save", "--bogus", "a"}, {"load", "-i"}, {"load", "a"},
		{"compose", "up", "a", "b"},
	} {
		if _, _, err := nyx(t, append([]string{"-socket", sock}, args...)...); err == nil {
			t.Errorf("nyx %v: accepted", args)
		}
	}
	// Accepted forms with a cooperative daemon.
	for _, args := range [][]string{
		{"run", "-d", "--print-id=true", "img"},
		{"image", "tag", "a", "b"},
		{"compose", "stop", "-f", writeCompose(t), "--project", "p"},
	} {
		if _, errOut, err := nyx(t, append([]string{"-socket", sock}, args...)...); err != nil {
			t.Errorf("nyx %v: %v %s", args, err, errOut)
		}
	}
	for _, sub := range []string{"push", "save", "load", "build"} {
		_, _, _ = nyx(t, "-socket", sock, "image", sub) // dispatch only
	}
}

func writeCompose(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "compose.yaml")
	_ = os.WriteFile(p, []byte("services:\n  s:\n    image: x\n"), 0o644)
	return p
}

func TestSaveLoadStdio(t *testing.T) {
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/save": reply(200, "archive-bytes"),
		"/load": reply(200, `{"ok":true,"refs":["a:1"]}`),
	}))
	// save to stdout (a pipe, not a terminal)
	if out, _, err := nyx(t, "-socket", sock, "save", "a:1"); err != nil || out != "archive-bytes" {
		t.Errorf("save to stdout: %q %v", out, err)
	}
	// save to a file in a missing directory
	if _, _, err := nyx(t, "-socket", sock, "save", "-o", filepath.Join(t.TempDir(), "no", "x.tar"), "a:1"); err == nil {
		t.Error("save into a missing dir")
	}
	// load from stdin; empty stdin is refused
	if out, _, err := nyxIn(t, "tar-bytes", "-socket", sock, "load"); err != nil || !strings.Contains(out, "a:1") {
		t.Errorf("load from stdin: %q %v", out, err)
	}
}

func TestRegistryCredentials(t *testing.T) {
	t.Setenv("NYX_REGISTRY_USERNAME", "envuser")
	t.Setenv("NYX_REGISTRY_PASSWORD", "envpass")
	if u, p := registryCredentials("x"); u != "envuser" || p != "envpass" {
		t.Error("env credentials")
	}
	t.Setenv("NYX_REGISTRY_USERNAME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", home)
	t.Setenv("NYX_AUTH_FILE", "")
	t.Setenv("REGISTRY_AUTH_FILE", "")
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	if u, _ := registryCredentials("x"); u != "" {
		t.Error("no config")
	}
	auth := base64.StdEncoding.EncodeToString([]byte("me:secret"))
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"https://index.docker.io/v1/":{"auth":"`+auth+`"},"reg.example:5000":{"auth":"!!"},"bad.example":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("nocolon"))+`"}}}`), 0o644)
	if u, p := registryCredentials("alpine"); u != "me" || p != "secret" {
		t.Errorf("docker hub: %q %q", u, p)
	}
	for _, ref := range []string{"reg.example:5000/a", "bad.example/a", "localhost/a"} {
		if u, _ := registryCredentials(ref); u != "" {
			t.Errorf("%s: %q", ref, u)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644)
	if u, _ := registryCredentials("x"); u != "" {
		t.Error("broken config")
	}
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("HOME", "")
	_, _ = registryCredentials("x")
	if humanSize(2048) != "2.0 kB" {
		t.Errorf("humanSize %q", humanSize(2048))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestExecFilters(t *testing.T) {
	var b bytes.Buffer
	f := newNyxdExecTrailerFilter(&b)
	_, _ = f.Write([]byte("keep\n\nnyxd: exec: x\npartial"))
	_ = f.Flush()
	_ = f.Flush()
	if b.String() != "keep\npartial\n" {
		t.Errorf("trailer filter: %q", b.String())
	}
	if _, err := newNyxdExecTrailerFilter(failingWriter{}).Write([]byte("x\n")); err == nil {
		t.Error("write error")
	}
	b.Reset()
	g := newBashExecCleanFilter(&b)
	_, _ = g.Write([]byte("\r\nnyxd: exec: y\nexec: \"bash\": executable file not found in $PATH\nout\ntail"))
	_ = g.Flush()
	_ = g.Flush()
	if !strings.Contains(b.String(), "executable file not found") || !strings.Contains(b.String(), "out\ntail\n") || strings.Contains(b.String(), "nyxd:") {
		t.Errorf("bash filter: %q", b.String())
	}
	if _, err := newBashExecCleanFilter(failingWriter{}).Write([]byte("x\n")); err == nil {
		t.Error("bash filter write error")
	}
	if _, err := newBashExecCleanFilter(failingWriter{}).Write([]byte("bash: executable file x\n")); err == nil {
		t.Error("bash filter write error on the hint line")
	}
	if string(stripToExecutableFileLine([]byte("  plain  "))) != "plain" {
		t.Error("strip without marker")
	}
	if err := execFailureFromTail([]byte("x\nnyxd: exec: \n")); err == nil || err.Error() != "exec failed" {
		t.Errorf("empty failure message: %v", err)
	}
	if err := execFailureFromTail([]byte("nyxd: exec: boom\nmore")); err == nil || err.Error() != "boom" {
		t.Errorf("failure with trailing output: %v", err)
	}
}
