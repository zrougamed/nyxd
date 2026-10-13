package image

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AuthMap holds registry credentials keyed by registry host (as from
// [RegistryHost]). nyx sends one with requests that may pull from several
// registries (compose up, build); the daemon never stores it.
type AuthMap map[string]RegistryAuth

// For returns the credentials for ref's registry, or nil.
func (m AuthMap) For(ref string) *RegistryAuth {
	if len(m) == 0 {
		return nil
	}
	a, ok := m[RegistryHost(ref)]
	if !ok || a.Username == "" {
		return nil
	}
	return &a
}

// RegistryHost is the registry of an image reference, normalized so every
// Docker Hub spelling is "docker.io".
func RegistryHost(ref string) string {
	reg, _, _ := parseRef(ref)
	return NormalizeRegistryHost(reg)
}

// NormalizeRegistryHost turns a registry as written in a ref, a login command
// or an auth file key ("https://index.docker.io/v1/", "quay.io/org") into a
// bare host, with Docker Hub's aliases mapped to "docker.io".
func NormalizeRegistryHost(s string) string {
	h := strings.ToLower(strings.TrimSpace(s))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h, _, _ = strings.Cut(h, "/")
	switch h {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io", "www.docker.io",
		"registry.docker.io", "hub.docker.com", "registry.hub.docker.com":
		return "docker.io"
	}
	return h
}

// CheckLogin verifies credentials against a registry the way `docker login`
// does: GET /v2/, answering the auth challenge with them.
func CheckLogin(ctx context.Context, registry string, auth RegistryAuth) error {
	reg := normalizeDockerHubRegistry(NormalizeRegistryHost(registry))
	s := newSession(newPullHTTPClient(), reg, "", &auth, "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL()+"/v2/", nil)
	if err != nil {
		return err
	}
	resp, err := s.do(req)
	if err != nil {
		if strings.Contains(err.Error(), "token request: 401") {
			return fmt.Errorf("login to %s: unauthorized: wrong username or password", registry)
		}
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("login to %s: unauthorized: wrong username or password", registry)
	default:
		return fmt.Errorf("login to %s: %s", registry, resp.Status)
	}
}
