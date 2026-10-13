package image

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// session talks to one repository of a registry (OCI distribution API) and
// handles authentication the standard way: a request that gets 401 is
// retried once with credentials obtained from the WWW-Authenticate
// challenge (Bearer token from the realm, or Basic).
type session struct {
	hc       *http.Client
	registry string
	repo     string
	user     string
	pass     string
	actions  string // token scope actions: "pull" or "pull,push"

	mu    sync.Mutex
	authz string // Authorization header value once known
}

func newSession(hc *http.Client, registry, repo string, auth *RegistryAuth, actions string) *session {
	s := &session{hc: hc, registry: registry, repo: repo, actions: actions}
	if auth != nil {
		s.user, s.pass = auth.Username, auth.Password
	}
	return s
}

// baseURL is https://<registry>, or http:// for loopback registries
// (localhost:5000 and friends), which Docker also treats as insecure-allowed.
func (s *session) baseURL() string {
	if isLoopbackRegistry(s.registry) {
		return "http://" + s.registry
	}
	return "https://" + s.registry
}

func isLoopbackRegistry(reg string) bool {
	host := reg
	if h, _, err := net.SplitHostPort(reg); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// repoURL builds <base>/v2/<repo>/<suffix>.
func (s *session) repoURL(suffix string) string {
	return fmt.Sprintf("%s/v2/%s/%s", s.baseURL(), s.repo, suffix)
}

// do sends req with the current credentials. On 401 it answers the
// challenge and retries once; requests with a body must set GetBody.
func (s *session) do(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	authz := s.authz
	s.mu.Unlock()
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := s.hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) //nolint:errcheck
	resp.Body.Close()

	authz, err = s.answer(req.Context(), challenge)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.authz = authz
	s.mu.Unlock()

	retry := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, errors.New("registry: cannot retry request body after authentication")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	retry.Header.Set("Authorization", authz)
	return s.hc.Do(retry)
}

// answer turns a WWW-Authenticate challenge into an Authorization value.
func (s *session) answer(ctx context.Context, challenge string) (string, error) {
	scheme, params := parseChallenge(challenge)
	switch strings.ToLower(scheme) {
	case "basic":
		if s.user == "" {
			return "", fmt.Errorf("registry %s requires credentials", s.registry)
		}
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(s.user+":"+s.pass)), nil
	case "bearer":
		realm := params["realm"]
		if realm == "" {
			return "", fmt.Errorf("registry %s: bearer challenge without realm", s.registry)
		}
		u, err := url.Parse(realm)
		if err != nil {
			return "", fmt.Errorf("registry %s: bad realm %q", s.registry, realm)
		}
		q := u.Query()
		if svc := params["service"]; svc != "" {
			q.Set("service", svc)
		}
		if s.repo != "" { // no scope: a login check
			q.Set("scope", fmt.Sprintf("repository:%s:%s", s.repo, s.actions))
		}
		u.RawQuery = q.Encode()
		tokenReq := func(withCreds bool) (*http.Response, error) {
			treq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				return nil, err
			}
			if withCreds {
				treq.SetBasicAuth(s.user, s.pass)
			}
			return s.hc.Do(treq)
		}
		resp, err := tokenReq(s.user != "")
		if err != nil {
			return "", err
		}
		// A login the token service rejects (expired or revoked, or Docker
		// Hub refusing it now and then) must not break public pulls: ask
		// again anonymously; a private repository is still denied.
		if resp.StatusCode == http.StatusUnauthorized && s.user != "" && !strings.Contains(s.actions, "push") && s.repo != "" {
			resp.Body.Close()
			if resp, err = tokenReq(false); err != nil {
				return "", err
			}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("registry %s: token request: %s", s.registry, resp.Status)
		}
		var tok struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
			return "", fmt.Errorf("registry %s: token response: %w", s.registry, err)
		}
		t := tok.Token
		if t == "" {
			t = tok.AccessToken
		}
		if t == "" {
			return "", fmt.Errorf("registry %s: empty token", s.registry)
		}
		return "Bearer " + t, nil
	default:
		return "", fmt.Errorf("registry %s: unsupported auth challenge %q", s.registry, challenge)
	}
}

// parseChallenge parses `Bearer realm="...",service="...",scope="..."`.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		k, v, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				params[k] = v[1:]
				break
			}
			params[k] = v[1 : end+1]
			rest = v[end+2:]
		} else {
			val, r, _ := strings.Cut(v, ",")
			params[k] = val
			rest = r
		}
	}
	return scheme, params
}
