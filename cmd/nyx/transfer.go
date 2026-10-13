package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/registryauth"
	"golang.org/x/term"
)

// streamClient has no overall timeout: pushes and archives can be large.
func streamClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialEndpoint(ctx, socket)
		},
	}}
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return daemonError(resp.StatusCode, b)
}

// daemonError is an error response from nyxd as the user should read it: the
// daemon's message alone (from a JSON {"error"|"message"} body or plain
// text), never the HTTP status line, which only says the request failed.
func daemonError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if strings.HasPrefix(msg, "{") {
		var m struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(msg), &m) == nil {
			if m.Error != "" {
				msg = m.Error
			} else if m.Message != "" {
				msg = m.Message
			}
		}
	}
	if msg == "" {
		switch status {
		case http.StatusNotFound:
			msg = "not found (is nyxd older than this nyx? try updating it)"
		case http.StatusServiceUnavailable:
			msg = "nyxd cannot do this right now (service unavailable)"
		default:
			msg = "nyxd: " + strings.ToLower(http.StatusText(status))
		}
	}
	return errors.New(msg)
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
// NYX_REGISTRY_USERNAME/NYX_REGISTRY_PASSWORD, else from a nyx, podman or
// docker login (see [registryauth.Lookup]).
func registryCredentials(ref string) (string, string) {
	if u := os.Getenv("NYX_REGISTRY_USERNAME"); u != "" {
		return u, os.Getenv("NYX_REGISTRY_PASSWORD")
	}
	a, _, _, err := registryauth.Lookup(image.RegistryHost(ref))
	warnAuth(err)
	return a.Username, a.Password
}

// registryAuthFor returns the logins for the registries of refs, to send
// with requests that pull from several registries.
func registryAuthFor(refs ...string) image.AuthMap {
	m, err := registryauth.For(refs...)
	warnAuth(err)
	return m
}

// warnAuth reports an unreadable auth file or failing credential helper; the
// request goes on (anonymously for that registry).
func warnAuth(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "nyx: warning: registry credentials: %v\n", err)
	}
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
