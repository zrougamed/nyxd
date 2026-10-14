package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/image"
)

type composeCLI struct {
	file          string
	project       string
	removeVolumes bool
	build         bool
	positional    []string
}

func parseComposeArgs(args []string) (composeCLI, error) {
	var c composeCLI
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--file":
			if i+1 >= len(args) {
				return composeCLI{}, fmt.Errorf("%s requires a path", a)
			}
			i++
			c.file = args[i]
		case strings.HasPrefix(a, "-f="):
			c.file = strings.TrimPrefix(a, "-f=")
		case strings.HasPrefix(a, "--file="):
			c.file = strings.TrimPrefix(a, "--file=")
		case a == "--project":
			if i+1 >= len(args) {
				return composeCLI{}, fmt.Errorf("--project requires a value")
			}
			i++
			c.project = args[i]
		case strings.HasPrefix(a, "--project="):
			c.project = strings.TrimPrefix(a, "--project=")
		case a == "-v" || a == "--volumes":
			c.removeVolumes = true
		case a == "--build":
			c.build = true
		case strings.HasPrefix(a, "-"):
			return composeCLI{}, fmt.Errorf("unknown flag %q", a)
		default:
			c.positional = append(c.positional, a)
		}
	}
	return c, nil
}

func resolveComposePathForCLI(flagPath string) (string, error) {
	if strings.TrimSpace(flagPath) != "" {
		return filepath.Abs(flagPath)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return compose.DefaultComposePath(wd)
}

func doCompose(socket string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf(`usage: nyx compose <up|stop|down> [flags]

flags:
  -f, --file PATH     compose file (default: first match in cwd — nyx-compose.yaml,
                      docker-compose.yml, compose.yaml, podman-compose.yml, and .yaml variants)
  --project NAME      stack prefix for container IDs and volumes (must match across up/stop/down).
                      If omitted, defaults to the compose file's parent directory name.
  -v, --volumes       (down only) delete declared named volume host dirs after remove
  --build             (up only) rebuild every service with build: (default: only missing images)

examples:
  nyx compose up
  nyx compose stop --project myapp
  nyx compose down -v`)
	}
	sub := args[0]
	opts, err := parseComposeArgs(args[1:])
	if err != nil {
		return err
	}
	if len(opts.positional) > 0 {
		return fmt.Errorf("unexpected argument %q", opts.positional[0])
	}
	switch sub {
	case "up":
		if opts.removeVolumes {
			return fmt.Errorf("compose up does not take -v/--volumes (use compose down -v)")
		}
		return doComposeUp(socket, opts)
	case "stop", "down":
		if opts.build {
			return fmt.Errorf("--build is only for compose up")
		}
		if sub == "down" {
			return doComposeDown(socket, opts)
		}
		if opts.removeVolumes {
			return fmt.Errorf("compose stop does not take -v/--volumes (use compose down -v)")
		}
		return doComposeStop(socket, opts)
	default:
		return fmt.Errorf("unknown compose subcommand %q (supported: up, stop, down)", sub)
	}
}

func doComposeUp(socket string, opts composeCLI) error {
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	var auth image.AuthMap
	// A path that exists only in the WSL distro (/mnt/c/...) is read by nyxd
	// alone: nothing to build or log in for here.
	if _, statErr := os.Stat(abs); !isWSLEndpoint(socket) || statErr == nil {
		if auth, err = composeBuild(socket, abs, opts); err != nil {
			return err
		}
	}
	// The daemon reads the file: give it the path as the daemon sees it.
	if abs, err = daemonPath(socket, abs); err != nil {
		return err
	}
	body := map[string]any{"file": abs}
	if opts.project != "" {
		body["project"] = opts.project
	}
	if len(auth) > 0 {
		body["registry_auth"] = auth
	}
	return postComposeJSON(socket, "http://unix/v1/compose/up", body, "ids", "Starting compose stack…")
}

func doComposeStop(socket string, opts composeCLI) error {
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	// The daemon reads the file: give it the path as the daemon sees it.
	if abs, err = daemonPath(socket, abs); err != nil {
		return err
	}
	body := map[string]string{"file": abs}
	if opts.project != "" {
		body["project"] = opts.project
	}
	return postComposeJSON(socket, "http://unix/v1/compose/stop", body, "stopped", "Stopping containers…")
}

func doComposeDown(socket string, opts composeCLI) error {
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	// The daemon reads the file: give it the path as the daemon sees it.
	if abs, err = daemonPath(socket, abs); err != nil {
		return err
	}
	body := map[string]any{"file": abs}
	if opts.project != "" {
		body["project"] = opts.project
	}
	if opts.removeVolumes {
		body["remove_volumes"] = true
	}
	b, err := postComposePOST(socket, "http://unix/v1/compose/down", body, "Stopping and removing containers…")
	if err != nil {
		return fmt.Errorf("compose down: %w", err)
	}
	var out struct {
		OK                 bool     `json:"ok"`
		Removed            []string `json:"removed"`
		RemovedVolumePaths []string `json:"removed_volume_paths"`
	}
	if err := json.Unmarshal(b, &out); err != nil || !out.OK {
		return fmt.Errorf("compose down: bad response: %s", bytes.TrimSpace(b))
	}
	if n := len(out.Removed); n > 0 {
		fmt.Fprintf(os.Stderr, "Stopped %d container(s): %s\n", n, strings.Join(out.Removed, ", "))
	}
	for _, id := range out.Removed {
		fmt.Println(id)
	}
	for _, p := range out.RemovedVolumePaths {
		fmt.Fprintf(os.Stderr, "removed volume dir %s\n", p)
	}
	return nil
}

// postComposePOST sends a compose JSON POST and returns the OK response body.
func postComposePOST(socket, url string, body any, spinner string) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	c := httpClient(socket)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp *http.Response
	err = withStderrSpinner(spinner, func() error {
		var e error
		resp, e = c.Do(req)
		return e
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, daemonError(resp.StatusCode, b)
	}
	return b, nil
}

func postComposeJSON(socket, url string, body any, listKey, spinner string) error {
	b, err := postComposePOST(socket, url, body, spinner)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("bad response: %s", bytes.TrimSpace(b))
	}
	if ok, _ := out["ok"].(bool); !ok {
		return fmt.Errorf("bad response: %s", bytes.TrimSpace(b))
	}
	if ws, ok := out["warnings"].([]any); ok {
		for _, w := range ws {
			if s, ok := w.(string); ok {
				fmt.Fprintf(os.Stderr, "warning: %s\n", s)
			}
		}
	}
	rawList, ok := out[listKey].([]any)
	if !ok {
		return nil
	}
	if n := len(rawList); n > 0 && spinner != "" {
		label := "Stopped"
		if strings.Contains(spinner, "Starting") {
			label = "Started"
		}
		ids := make([]string, 0, n)
		for _, v := range rawList {
			if s, ok := v.(string); ok {
				ids = append(ids, s)
			}
		}
		fmt.Fprintf(os.Stderr, "%s %d container(s): %s\n", label, n, strings.Join(ids, ", "))
	}
	for _, v := range rawList {
		if s, ok := v.(string); ok {
			fmt.Println(s)
		}
	}
	return nil
}

// composeBuild builds the images of services with build: before the stack
// starts: the ones not in the daemon's store, or all of them with --build.
// The client builds (like docker compose) so the context is read here and the
// output streams to this terminal. It returns the logins for the registries
// of the images the daemon may pull.
func composeBuild(socket, abs string, opts composeCLI) (image.AuthMap, error) {
	st, err := compose.ParseFile(abs)
	if err != nil {
		return nil, err
	}
	project := opts.project
	if project == "" {
		project = compose.ProjectName(st, abs)
	}
	st.SetBuildImages(project)
	for _, name := range st.BuildServices() {
		svc := st.Services[name]
		if !opts.build {
			ok, err := imageExists(socket, svc.Image)
			if err != nil {
				return nil, err
			}
			if ok {
				continue
			}
		}
		ctxDir, err := svc.Build.ContextDir(filepath.Dir(abs))
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		if fi, err := os.Stat(ctxDir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("service %q: build context %s is not a directory", name, ctxDir)
		}
		o := buildOpts{tags: []string{svc.Image}, target: svc.Build.Target}
		for k, v := range svc.Build.Args {
			o.buildArgs = append(o.buildArgs, k+"="+v)
		}
		sort.Strings(o.buildArgs)
		fmt.Fprintf(os.Stderr, "Building %s (%s)\n", name, svc.Image)
		if err := runBuild(socket, o, ctxDir, svc.Build.DockerfilePath(ctxDir)); err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
	}
	var pulled []string
	for _, svc := range st.Services {
		if svc.Build == nil && strings.TrimSpace(svc.RegistryUsername) == "" {
			pulled = append(pulled, svc.Image)
		}
	}
	sort.Strings(pulled)
	return registryAuthFor(pulled...), nil
}

// imageExists reports whether the daemon has ref in its image store.
func imageExists(socket, ref string) (bool, error) {
	resp, err := httpClient(socket).Get("http://unix/v1/images/inspect?ref=" + url.QueryEscape(ref))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		b, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("image %s: %w", ref, daemonError(resp.StatusCode, b))
	}
}
