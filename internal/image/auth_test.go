package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authRegistry answers /v2/ only with valid credentials, through a Basic or
// a Bearer (token endpoint) challenge.
func authRegistry(t *testing.T, scheme string) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Has("scope") {
				t.Errorf("login check sent a scope: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case r.URL.Path == "/v2/":
			got := r.Header.Get("Authorization")
			if scheme == "basic" {
				if u, p, ok := r.BasicAuth(); ok && u == "me" && p == "pw" {
					return
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="reg"`)
			} else {
				if got == "Bearer tok" {
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg"`)
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckLogin(t *testing.T) {
	ctx := context.Background()
	for _, scheme := range []string{"basic", "bearer"} {
		host := strings.TrimPrefix(authRegistry(t, scheme).URL, "http://")
		if err := CheckLogin(ctx, host, RegistryAuth{Username: "me", Password: "pw"}); err != nil {
			t.Errorf("%s good login: %v", scheme, err)
		}
		err := CheckLogin(ctx, host, RegistryAuth{Username: "me", Password: "nope"})
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Errorf("%s bad login: %v", scheme, err)
		}
	}
	open := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer open.Close()
	if err := CheckLogin(ctx, strings.TrimPrefix(open.URL, "http://"), RegistryAuth{Username: "a", Password: "b"}); err != nil {
		t.Errorf("open registry: %v", err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer broken.Close()
	if err := CheckLogin(ctx, strings.TrimPrefix(broken.URL, "http://"), RegistryAuth{}); err == nil {
		t.Error("418 accepted")
	}
}

func TestAuthMapAndHosts(t *testing.T) {
	for in, want := range map[string]string{
		"":                            "docker.io",
		"https://index.docker.io/v1/": "docker.io",
		"registry-1.docker.io":        "docker.io",
		"Quay.io/org/repo":            "quay.io",
		"http://localhost:5000":       "localhost:5000",
	} {
		if got := NormalizeRegistryHost(in); got != want {
			t.Errorf("NormalizeRegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
	m := AuthMap{"docker.io": {Username: "hub", Password: "x"}, "ghcr.io": {}}
	if a := m.For("docker.io/library/alpine:3"); a == nil || a.Username != "hub" {
		t.Errorf("docker hub: %v", a)
	}
	if m.For("ghcr.io/a/b") != nil || m.For("quay.io/a") != nil || AuthMap(nil).For("x") != nil {
		t.Error("empty or missing entry used")
	}
}

// The token service rejecting a saved login must not break a public pull:
// the token is asked for again anonymously. A login check still fails.
func TestRejectedLoginFallsBackToAnonymous(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if _, _, ok := r.BasicAuth(); ok {
				w.WriteHeader(http.StatusUnauthorized) // stale credentials
				return
			}
			if !r.URL.Query().Has("scope") {
				w.WriteHeader(http.StatusUnauthorized) // no anonymous login check
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "anon"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer anon" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	stale := &RegistryAuth{Username: "me", Password: "expired"}

	s := newSession(newPullHTTPClient(), host, "library/alpine", stale, "pull")
	req, _ := http.NewRequest(http.MethodGet, s.baseURL()+"/v2/library/alpine/manifests/3", nil)
	resp, err := s.do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("pull with a rejected login: %v %v", resp, err)
	}
	resp.Body.Close()

	push := newSession(newPullHTTPClient(), host, "library/alpine", stale, "pull,push")
	req, _ = http.NewRequest(http.MethodGet, push.baseURL()+"/v2/library/alpine/manifests/3", nil)
	if _, err := push.do(req); err == nil {
		t.Error("push session fell back to anonymous")
	}
	if err := CheckLogin(context.Background(), host, *stale); err == nil {
		t.Error("login check accepted a rejected login")
	}
}
