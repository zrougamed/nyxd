package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// streamClient has no overall timeout: pushes and archives can be large.
func streamClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
}

func doTag(socket string, args []string) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return errors.New("usage: nyx tag <source-ref> <target-ref>")
	}
	body, _ := json.Marshal(map[string]string{"source": args[0], "target": args[1]})
	resp, err := httpClient(socket).Post("http://unix/v1/images/tag", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tag: %w", apiError(resp))
	}
	return nil
}

func doPush(socket string, args []string) error {
	const usage = "usage: nyx push [--username USER] [--password PASS] <ref>"
	var ref, user, pass string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-u", "--username", "--password":
			if i+1 >= len(args) {
				return errors.New(usage)
			}
			i++
			if a == "--password" {
				pass = args[i]
			} else {
				user = args[i]
			}
		default:
			if strings.HasPrefix(a, "-") || ref != "" {
				return errors.New(usage)
			}
			ref = a
		}
	}
	if ref == "" {
		return errors.New(usage)
	}
	if user == "" {
		user, pass = registryCredentials(ref)
	}

	body, _ := json.Marshal(map[string]any{"ref": ref, "stream": true, "username": user, "password": pass})
	resp, err := streamClient(socket).Post("http://unix/v1/images/push", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push: %w", apiError(resp))
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var ev struct {
			Phase   string `json:"phase"`
			Message string `json:"message"`
			Digest  string `json:"digest"`
			Index   int    `json:"index"`
			Count   int    `json:"count"`
			Size    int64  `json:"size"`
			Cached  bool   `json:"cached"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		short := strings.TrimPrefix(ev.Digest, "sha256:")
		if len(short) > 12 {
			short = short[:12]
		}
		switch ev.Phase {
		case "blob":
			state := "pushing"
			if ev.Cached {
				state = "exists "
			}
			fmt.Printf("[%d/%d] %s %s (%s)\n", ev.Index, ev.Count, state, short, humanSize(ev.Size))
		case "done":
			fmt.Printf("pushed %s@%s\n", ref, ev.Digest)
			return nil
		case "error":
			return fmt.Errorf("push failed: %s", ev.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("push: connection closed before the push finished")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// registryCredentials returns credentials for ref's registry from
// NYX_REGISTRY_USERNAME/NYX_REGISTRY_PASSWORD, else from the "auths" entries
// of ~/.docker/config.json (or $DOCKER_CONFIG/config.json), so an existing
// `docker login` works. Credential helpers are not executed.
func registryCredentials(ref string) (string, string) {
	if u := os.Getenv("NYX_REGISTRY_USERNAME"); u != "" {
		return u, os.Getenv("NYX_REGISTRY_PASSWORD")
	}
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ""
		}
		dir = filepath.Join(home, ".docker")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return "", ""
	}
	return dockerConfigAuth(data, registryHost(ref))
}

func dockerConfigAuth(configJSON []byte, host string) (string, string) {
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(configJSON, &cfg) != nil {
		return "", ""
	}
	keys := []string{host, "https://" + host, "http://" + host}
	if host == "docker.io" {
		keys = append(keys, "https://index.docker.io/v1/", "index.docker.io", "registry-1.docker.io")
	}
	for _, k := range keys {
		e, ok := cfg.Auths[k]
		if !ok || e.Auth == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(e.Auth)
		if err != nil {
			continue
		}
		if u, p, ok := strings.Cut(string(raw), ":"); ok {
			return u, p
		}
	}
	return "", ""
}

// registryHost is the registry part of an image reference ("docker.io" when
// the first path component is not a host).
func registryHost(ref string) string {
	first, _, found := strings.Cut(ref, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "docker.io"
}

func doSave(socket string, args []string) error {
	const usage = "usage: nyx save [-o FILE] <ref> [<ref>...]"
	var out string
	var refs []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				return errors.New(usage)
			}
			i++
			out = args[i]
		case strings.HasPrefix(a, "--output="):
			out = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-"):
			return errors.New(usage)
		default:
			refs = append(refs, a)
		}
	}
	if len(refs) == 0 {
		return errors.New(usage)
	}
	if out == "" && term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("refusing to write an archive to a terminal: use -o FILE or redirect stdout")
	}
	q := url.Values{"ref": refs}
	resp, err := streamClient(socket).Get("http://unix/v1/images/save?" + q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("save: %w", apiError(resp))
	}
	var w io.Writer = os.Stdout
	if out != "" {
		tmp := out + ".partial"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("save: %w", err)
		}
		if err := f.Close(); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, out)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func doLoad(socket string, args []string) error {
	const usage = "usage: nyx load [-i FILE]   (reads stdin without -i)"
	var in io.Reader = os.Stdin
	switch {
	case len(args) == 0:
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("no archive on stdin: use -i FILE or pipe one in")
		}
	case len(args) == 2 && (args[0] == "-i" || args[0] == "--input"):
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	default:
		return errors.New(usage)
	}
	resp, err := streamClient(socket).Post("http://unix/v1/images/load", "application/x-tar", in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("load: %w", apiError(resp))
	}
	var res struct {
		Refs []string `json:"refs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	for _, r := range res.Refs {
		fmt.Printf("Loaded image: %s\n", r)
	}
	return nil
}
