// Package registryauth finds registry credentials the way the usual tools
// store them, so a login made with any of them works for nyx:
//
//   - nyx:    $NYX_AUTH_FILE, else <config dir>/nyx/auth.json (written by nyx login)
//   - podman: $REGISTRY_AUTH_FILE, $XDG_RUNTIME_DIR/containers/auth.json,
//     <config dir>/containers/auth.json
//   - docker: $DOCKER_CONFIG/config.json, else ~/.docker/config.json
//
// All three share the "auths" format; credHelpers / credsStore entries run
// docker-credential-<name> like docker does. It is used by the nyx client
// only: the daemon never reads these files, nyx sends what a request needs.
package registryauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/image"
)

// File is one credentials file nyx reads.
type File struct {
	Path string
	Kind string // "nyx", "podman" or "docker"
}

// NyxFile is where nyx login stores credentials.
func NyxFile() (string, error) {
	if p := os.Getenv("NYX_AUTH_FILE"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nyx", "auth.json"), nil
}

// Files lists the credentials files in lookup order (nyx, podman, docker).
// Files that do not exist are included; Lookup skips them.
func Files() []File {
	var out []File
	if p, err := NyxFile(); err == nil {
		out = append(out, File{p, "nyx"})
	}
	if p := os.Getenv("REGISTRY_AUTH_FILE"); p != "" {
		out = append(out, File{p, "podman"})
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		out = append(out, File{filepath.Join(d, "containers", "auth.json"), "podman"})
	}
	if d, err := os.UserConfigDir(); err == nil {
		out = append(out, File{filepath.Join(d, "containers", "auth.json"), "podman"})
	}
	dd := os.Getenv("DOCKER_CONFIG")
	if dd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dd = filepath.Join(home, ".docker")
		}
	}
	if dd != "" {
		out = append(out, File{filepath.Join(dd, "config.json"), "docker"})
	}
	return out
}

// config is the part of config.json / auth.json nyx uses.
type config struct {
	Auths       map[string]authEntry `json:"auths"`
	CredHelpers map[string]string    `json:"credHelpers"`
	CredsStore  string               `json:"credsStore"`
}

type authEntry struct {
	Auth     string `json:"auth"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Lookup returns credentials for registry host (any spelling: "docker.io",
// "https://index.docker.io/v1/", "ghcr.io"), the file they came from, and
// whether any were found. err reports a file or credential helper that could
// not be read; the search goes on past it, so callers can warn and continue.
func Lookup(host string) (auth image.RegistryAuth, from string, found bool, err error) {
	host = image.NormalizeRegistryHost(host)
	var errs []error
	for _, f := range Files() {
		a, ok, e := lookupFile(f, host)
		if e != nil {
			errs = append(errs, e)
		}
		if ok {
			return a, f.Path, true, errors.Join(errs...)
		}
	}
	return image.RegistryAuth{}, "", false, errors.Join(errs...)
}

// For returns credentials for the registries of refs, found with Lookup.
func For(refs ...string) (image.AuthMap, error) {
	m := image.AuthMap{}
	var errs []error
	for _, ref := range refs {
		h := image.RegistryHost(ref)
		if _, done := m[h]; done {
			continue
		}
		a, _, ok, err := Lookup(h)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			m[h] = a
		}
	}
	return m, errors.Join(errs...)
}

func readConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func lookupFile(f File, host string) (image.RegistryAuth, bool, error) {
	c, err := readConfig(f.Path)
	if c == nil || err != nil {
		return image.RegistryAuth{}, false, err
	}
	for k, helper := range c.CredHelpers {
		if image.NormalizeRegistryHost(k) == host {
			return fromHelper(helper, host)
		}
	}
	// docker keeps only empty markers in auths when a credsStore is set.
	if e, ok := c.entry(host); ok {
		if a, ok, err := e.decode(); ok || err != nil {
			return a, ok, err
		}
	}
	if c.CredsStore != "" {
		return fromHelper(c.CredsStore, host)
	}
	return image.RegistryAuth{}, false, nil
}

// entry finds host in auths: an exact host key first, then any key for it
// (a URL, or podman's per-namespace "quay.io/org" keys).
func (c *config) entry(host string) (authEntry, bool) {
	if e, ok := c.Auths[host]; ok {
		return e, true
	}
	for k, e := range c.Auths {
		if image.NormalizeRegistryHost(k) == host && (e.Auth != "" || e.Username != "") {
			return e, true
		}
	}
	return authEntry{}, false
}

func (e authEntry) decode() (image.RegistryAuth, bool, error) {
	if e.Username != "" {
		return image.RegistryAuth{Username: e.Username, Password: e.Password}, true, nil
	}
	if e.Auth == "" {
		return image.RegistryAuth{}, false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(e.Auth)
	if err != nil {
		return image.RegistryAuth{}, false, fmt.Errorf("bad auth entry: %w", err)
	}
	u, p, ok := strings.Cut(string(raw), ":")
	if !ok {
		return image.RegistryAuth{}, false, errors.New("bad auth entry: no ':'")
	}
	return image.RegistryAuth{Username: u, Password: p}, true, nil
}

var helperName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// helperTimeout bounds a credential helper (it may wait on a keyring unlock).
var helperTimeout = 30 * time.Second

// fromHelper runs `docker-credential-<name> get` with the server URL docker
// would have stored the login under.
func fromHelper(name, host string) (image.RegistryAuth, bool, error) {
	if !helperName.MatchString(name) {
		return image.RegistryAuth{}, false, fmt.Errorf("credential helper %q: invalid name", name)
	}
	bin, err := exec.LookPath("docker-credential-" + name)
	if err != nil {
		return image.RegistryAuth{}, false, fmt.Errorf("credential helper docker-credential-%s not found in PATH", name)
	}
	server := host
	if host == "docker.io" {
		server = "https://index.docker.io/v1/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "get")
	cmd.Stdin = strings.NewReader(server)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stdout.String() + " " + stderr.String())
		if strings.Contains(strings.ToLower(msg), "credentials not found") {
			return image.RegistryAuth{}, false, nil
		}
		return image.RegistryAuth{}, false, fmt.Errorf("docker-credential-%s get %s: %v: %s", name, server, err, msg)
	}
	var out struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return image.RegistryAuth{}, false, fmt.Errorf("docker-credential-%s: %w", name, err)
	}
	if out.Username == "" || out.Username == "<token>" {
		// An identity token (OAuth refresh token) is not supported.
		return image.RegistryAuth{}, false, nil
	}
	return image.RegistryAuth{Username: out.Username, Password: out.Secret}, true, nil
}

// Save stores credentials for host in the auth file at path (the nyx file
// for nyx login), keeping every other entry and field.
func Save(path, host string, a image.RegistryAuth) error {
	host = image.NormalizeRegistryHost(host)
	return edit(path, func(auths map[string]json.RawMessage) error {
		e, err := json.Marshal(authEntry{Auth: base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))})
		if err != nil {
			return err
		}
		for k := range auths {
			if image.NormalizeRegistryHost(k) == host {
				delete(auths, k)
			}
		}
		auths[host] = e
		return nil
	})
}

// Remove deletes host's credentials from the auth file at path and reports
// whether there were any.
func Remove(path, host string) (bool, error) {
	host = image.NormalizeRegistryHost(host)
	var removed bool
	err := edit(path, func(auths map[string]json.RawMessage) error {
		for k := range auths {
			if image.NormalizeRegistryHost(k) == host {
				delete(auths, k)
				removed = true
			}
		}
		return nil
	})
	return removed, err
}

func edit(path string, change func(map[string]json.RawMessage) error) error {
	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &top); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	auths := map[string]json.RawMessage{}
	if raw, ok := top["auths"]; ok {
		if err := json.Unmarshal(raw, &auths); err != nil {
			return fmt.Errorf("%s: auths: %w", path, err)
		}
	}
	if err := change(auths); err != nil {
		return err
	}
	raw, err := json.Marshal(auths)
	if err != nil {
		return err
	}
	top["auths"] = raw
	out, err := json.MarshalIndent(top, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path) // CreateTemp makes it 0600
}
