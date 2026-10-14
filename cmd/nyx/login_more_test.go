package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/zrougamed/nyxd/internal/registryauth"
)

// loginRegistry accepts me/pw and isolates nyx's auth files under a
// temporary home. It returns the registry host.
func loginRegistry(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": home, "XDG_RUNTIME_DIR": home,
		"NYX_AUTH_FILE": "", "REGISTRY_AUTH_FILE": "", "DOCKER_CONFIG": filepath.Join(home, "docker"), "NYX_REGISTRY_USERNAME": ""} {
		t.Setenv(k, v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "me" && p == "pw" {
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// On a terminal, nyx login asks for the username and the password.
func TestLoginPrompts(t *testing.T) {
	host := loginRegistry(t)
	ptm, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptm.Close()
	defer tty.Close()
	go func() {
		buf := make([]byte, 4096)
		for { // drain the terminal: prompts and echo
			if _, err := ptm.Read(buf); err != nil {
				return
			}
		}
	}()
	stdioMu.Lock()
	old := os.Stdin
	os.Stdin = tty
	defer func() { os.Stdin = old; stdioMu.Unlock() }()
	go func() {
		ptm.Write([]byte("me\n")) //nolint:errcheck
		// The username is read through a buffer: send the password once
		// nyx reads it from the terminal directly.
		time.Sleep(300 * time.Millisecond)
		ptm.Write([]byte("pw\n")) //nolint:errcheck
	}()
	authfile := filepath.Join(t.TempDir(), "auth.json")
	if err := doLogin([]string{"--authfile=" + authfile, host}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(authfile); !strings.Contains(string(b), host) {
		t.Errorf("auth file:\n%s", b)
	}
}

func TestLoginErrors(t *testing.T) {
	host := loginRegistry(t)
	dir := t.TempDir()
	for _, c := range []struct {
		stdin string
		args  []string
		want  string
	}{
		{"", []string{host}, "no username"},
		{"", []string{"-u", "me", host}, "no password"},
		{"", []string{"--username=me", "-p", "", host}, "username and password are required"},
		{"", []string{"-h"}, "usage"},
		{"", []string{"--authfile"}, "requires a value"},
		{"pw\n", []string{"-u", "me", "--password-stdin", "--authfile", dir, host}, "save credentials"},
	} {
		_, _, err := capture(t, c.stdin, func() error { return doLogin(c.args) })
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("login %v: %v, want %q", c.args, err, c.want)
		}
	}

	// --password works, with a warning.
	_, errOut, err := capture(t, "", func() error { return doLogin([]string{"-u", "me", "--password=pw", host}) })
	if err != nil || !strings.Contains(errOut, "insecure") {
		t.Errorf("--password: %v %q", err, errOut)
	}
	authfile := filepath.Join(t.TempDir(), "a.json")
	if _, _, err := capture(t, "", func() error { return doLogin([]string{"-u", "me", "-p", "pw", "--authfile", authfile, host}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := capture(t, "", func() error { return doLogout([]string{"--authfile=" + authfile, host}) }); err != nil {
		t.Errorf("logout --authfile=: %v", err)
	}
	for _, args := range [][]string{{"--authfile"}, {"-x"}, {"a", "b"}} {
		if err := doLogout(args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("logout %v: %v", args, err)
		}
	}
	if err := doLogout([]string{"--authfile", dir, host}); err == nil {
		t.Error("logout from a directory")
	}

	// No home directory: nowhere to save.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := registryauth.NyxFile(); err == nil {
		t.Skip("NyxFile has a fallback without a home directory")
	}
	if _, _, err := capture(t, "", func() error { return doLogin([]string{"-u", "me", "-p", "pw", host}) }); err == nil {
		t.Error("login without a home directory")
	}
	if err := doLogout([]string{host}); err == nil {
		t.Error("logout without a home directory")
	}
}
