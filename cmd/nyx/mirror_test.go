package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorLoginsSentWithPull(t *testing.T) {
	authFile := filepath.Join(t.TempDir(), "auth.json")
	cred := base64.StdEncoding.EncodeToString([]byte("mirroruser:mirrorpass"))
	if err := os.WriteFile(authFile, []byte(`{"auths":{"mirror.lan:5000":{"auth":"`+cred+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NYX_AUTH_FILE", authFile)
	t.Setenv("REGISTRY_AUTH_FILE", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	var pullBody string
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/v1/info": reply(200, `{"registry_mirrors":{"registry-1.docker.io":["https://mirror.lan:5000","https://other.lan"]}}`),
		"/v1/images/pull": func(w http.ResponseWriter, r *http.Request) {
			var b bytes.Buffer
			_, _ = b.ReadFrom(r.Body)
			pullBody = b.String()
			_, _ = w.Write([]byte(`{"ok":true,"ref":"alpine:3.20"}`))
		},
	}))
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "pull", "--json", "alpine:3.20"}) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pullBody, `"registry_auth":{"mirror.lan:5000":{"username":"mirroruser","password":"mirrorpass"}`) || strings.Contains(pullBody, "other.lan") {
		t.Fatalf("pull body: %s", pullBody)
	}
	// No mirrors, or an old daemon: nothing extra is sent.
	if m := mirrorLogins(stub(t, reply(404, "404 page not found"))); len(m) != 0 {
		t.Fatalf("old daemon: %v", m)
	}
	if m := withMirrorLogins(stub(t, reply(200, `{}`)), nil); m != nil {
		t.Fatalf("no mirrors: %v", m)
	}
}
