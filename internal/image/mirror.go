package image

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Mirror is a registry mirror: pulls for its registry try it first.
type Mirror struct {
	URL    string // as configured, for messages
	Scheme string // http or https
	Host   string // host[:port]
	Prefix string // path prefix in front of the repository (proxy projects), may be empty
}

// ParseMirror reads "REGISTRY=URL" (nyxd -registry-mirror) and returns the
// registry API host it applies to (docker.io is registry-1.docker.io).
func ParseMirror(spec string) (string, Mirror, error) {
	reg, raw, ok := strings.Cut(spec, "=")
	reg, raw = strings.TrimSpace(reg), strings.TrimSpace(raw)
	if !ok || reg == "" || raw == "" {
		return "", Mirror{}, fmt.Errorf("registry mirror %q: want REGISTRY=URL, e.g. docker.io=https://mirror.lan:5000", spec)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", Mirror{}, fmt.Errorf("registry mirror %q: URL must be http(s)://host[:port][/prefix]", spec)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", Mirror{}, fmt.Errorf("registry mirror %q: no credentials, query or fragment in the URL (use nyx login for the mirror host)", spec)
	}
	return normalizeDockerHubRegistry(reg), Mirror{
		URL:    raw,
		Scheme: u.Scheme,
		Host:   u.Host,
		Prefix: strings.Trim(u.Path, "/"),
	}, nil
}

// SetMirrors sets the registry mirrors (by registry API host, tried in
// order) and whether a registry may still be used when all its mirrors
// fail. Call before pulling.
func (s *Store) SetMirrors(m map[string][]Mirror, mirrorOnly bool) {
	s.mirrors, s.mirrorOnly = m, mirrorOnly
}

// Mirrors returns the configured mirror URLs by registry (for nyx info).
func (s *Store) Mirrors() map[string][]string {
	out := map[string][]string{}
	for reg, ms := range s.mirrors {
		for _, m := range ms {
			out[reg] = append(out[reg], m.URL)
		}
	}
	return out
}

// MirrorOnly reports -registry-mirror-only.
func (s *Store) MirrorOnly() bool { return s.mirrorOnly }

func (s *Store) mirrorsFor(registry string) []Mirror { return s.mirrors[registry] }

// MirrorHosts lists the hosts of all mirrors, sorted.
func (s *Store) MirrorHosts() []string {
	seen := map[string]bool{}
	var out []string
	for _, ms := range s.mirrors {
		for _, m := range ms {
			if !seen[m.Host] {
				seen[m.Host] = true
				out = append(out, m.Host)
			}
		}
	}
	sort.Strings(out)
	return out
}

type mirrorAuthKey struct{}

// WithMirrorAuth attaches credentials by mirror host to ctx: the client
// sends its logins for the mirror hosts along with a pull, and the mirror
// gets those, never the upstream registry's.
func WithMirrorAuth(ctx context.Context, m AuthMap) context.Context {
	if len(m) == 0 {
		return ctx
	}
	return context.WithValue(ctx, mirrorAuthKey{}, m)
}

// MirrorAuthFrom returns the credentials attached by WithMirrorAuth.
func MirrorAuthFrom(ctx context.Context) AuthMap {
	m, _ := ctx.Value(mirrorAuthKey{}).(AuthMap)
	return m
}

func (m AuthMap) host(host string) *RegistryAuth {
	a, ok := m[host]
	if !ok || a.Username == "" {
		return nil
	}
	return &a
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
