package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/registryauth"
)

func withStdin(t *testing.T, s string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { w.WriteString(s); w.Close() }() //nolint:errcheck
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

func TestLoginLogout(t *testing.T) {
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
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	withStdin(t, "wrong\n")
	if err := doLogin([]string{"-u", "me", "--password-stdin", host}); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("bad password: %v", err)
	}
	withStdin(t, "pw\n")
	if err := doLogin([]string{"-u", "me", "--password-stdin", host}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if u, p := registryCredentials(host + "/team/app:1"); u != "me" || p != "pw" {
		t.Errorf("after login: %q %q", u, p)
	}
	nyxFile, _ := registryauth.NyxFile()
	if _, err := os.Stat(nyxFile); err != nil {
		t.Errorf("auth file: %v", err)
	}

	for _, args := range [][]string{{"--password-stdin"}, {"-p", "x", "--password-stdin", "-u", "a"}, {"-x"}, {"a", "b"}, {"-u"}} {
		withStdin(t, "")
		if err := doLogin(args); err == nil {
			t.Errorf("login %v accepted", args)
		}
	}

	if err := doLogout([]string{host}); err != nil {
		t.Errorf("logout: %v", err)
	}
	if u, _ := registryCredentials(host + "/x"); u != "" {
		t.Error("still logged in")
	}
	if err := doLogout([]string{host}); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("second logout: %v", err)
	}
	// A docker login is reported, not removed.
	if err := os.MkdirAll(filepath.Join(home, "docker"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "docker", "config.json"), []byte(`{"auths":{"`+host+`":{"auth":"bWU6cHc="}}}`), 0o600) //nolint:errcheck
	if err := doLogout([]string{host}); err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("docker login logout: %v", err)
	}
}

func TestDockerfileBaseImages(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Dockerfile")
	os.WriteFile(p, []byte(`ARG V=1
FROM --platform=$BUILDPLATFORM ghcr.io/me/builder:1 AS build
FROM node AS node
from build AS again
FROM scratch
FROM alpine:$V
FROM quay.io/org/base
`), 0o600) //nolint:errcheck
	got := strings.Join(dockerfileBaseImages(p), ",")
	if got != "ghcr.io/me/builder:1,node,quay.io/org/base" {
		t.Errorf("base images = %s", got)
	}
	if dockerfileBaseImages(filepath.Join(t.TempDir(), "none")) != nil {
		t.Error("missing Dockerfile")
	}
}
