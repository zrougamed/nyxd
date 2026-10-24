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
		return fmt.Errorf(`usage: nyx compose <up|stop|down|ps|stats> [flags]

flags:
  -f, --file PATH     compose file (default: first match in cwd — nyx-compose.yaml,
                      docker-compose.yml, compose.yaml, podman-compose.yml, and .yaml variants)
  --project NAME      stack prefix for container IDs and volumes (must match across up/stop/down/ps).
                      If omitted, defaults to the compose file's parent directory name.
  -v, --volumes       (down only) delete declared named volume host dirs after remove
  --build             (up only) rebuild every service with build: (default: only missing images)
  -q, --quiet         (ps only) print container ids only
  --no-stream         (stats only) print one sample and exit
  --no-trunc          (ps/stats) do not truncate container ids / names

examples:
  nyx compose up
  nyx compose ps
  nyx compose stop --project myapp
  nyx compose down -v
  nyx compose stats --no-stream
  nyx compose stats web`)
	}
	sub := args[0]
	switch sub {
	case "stats":
		return doComposeStats(socket, args[1:])
	case "ps":
		return doComposePs(socket, args[1:])
	}
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
		return fmt.Errorf("unknown compose subcommand %q (supported: up, stop, down, ps, stats)", sub)
	}
}

type composeStatsCLI struct {
	file     string
	project  string
	noStream bool
	noTrunc  bool
	services []string
}

func parseComposeStatsArgs(args []string) (composeStatsCLI, error) {
	var c composeStatsCLI
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--file":
			if i+1 >= len(args) {
				return c, fmt.Errorf("%s requires a path", a)
			}
			i++
			c.file = args[i]
		case strings.HasPrefix(a, "-f="):
			c.file = strings.TrimPrefix(a, "-f=")
		case strings.HasPrefix(a, "--file="):
			c.file = strings.TrimPrefix(a, "--file=")
		case a == "--project":
			if i+1 >= len(args) {
				return c, fmt.Errorf("--project requires a value")
			}
			i++
			c.project = args[i]
		case strings.HasPrefix(a, "--project="):
			c.project = strings.TrimPrefix(a, "--project=")
		case a == "--no-stream":
			c.noStream = true
		case a == "--no-trunc":
			c.noTrunc = true
		case a == "-a" || a == "--all":
			// reserved
		case a == "-v" || a == "--volumes" || a == "--build":
			return c, fmt.Errorf("%s is not valid for compose stats", a)
		case strings.HasPrefix(a, "-"):
			return c, fmt.Errorf("unknown flag %q", a)
		default:
			c.services = append(c.services, a)
		}
	}
	return c, nil
}

type composePsCLI struct {
	file     string
	project  string
	quiet    bool
	noTrunc  bool
	services []string
}

func parseComposePsArgs(args []string) (composePsCLI, error) {
	var c composePsCLI
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--file":
			if i+1 >= len(args) {
				return c, fmt.Errorf("%s requires a path", a)
			}
			i++
			c.file = args[i]
		case strings.HasPrefix(a, "-f="):
			c.file = strings.TrimPrefix(a, "-f=")
		case strings.HasPrefix(a, "--file="):
			c.file = strings.TrimPrefix(a, "--file=")
		case a == "--project":
			if i+1 >= len(args) {
				return c, fmt.Errorf("--project requires a value")
			}
			i++
			c.project = args[i]
		case strings.HasPrefix(a, "--project="):
			c.project = strings.TrimPrefix(a, "--project=")
		case a == "-q" || a == "--quiet":
			c.quiet = true
		case a == "--no-trunc":
			c.noTrunc = true
		case a == "-v" || a == "--volumes" || a == "--build" || a == "--no-stream":
			return c, fmt.Errorf("%s is not valid for compose ps", a)
		case strings.HasPrefix(a, "-"):
			return c, fmt.Errorf("unknown flag %q", a)
		default:
			c.services = append(c.services, a)
		}
	}
	return c, nil
}

// doComposePs lists compose services and their container status (like docker compose ps).
func doComposePs(socket string, args []string) error {
	opts, err := parseComposePsArgs(args)
	if err != nil {
		return err
	}
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	st, err := compose.ParseFile(abs)
	if err != nil {
		return fmt.Errorf("compose: %w", err)
	}
	project := strings.TrimSpace(opts.project)
	if project == "" {
		project = compose.ProjectName(st, abs)
	}

	type row struct {
		service string
		id      string
	}
	var rows []row
	if len(opts.services) == 0 {
		for _, name := range compose.TopologicalOrder(st) {
			rows = append(rows, row{service: name, id: compose.ContainerID(project, name)})
		}
	} else {
		known := make(map[string]struct{}, len(st.Services))
		for name := range st.Services {
			known[name] = struct{}{}
		}
		for _, svc := range opts.services {
			if _, ok := known[svc]; !ok {
				return fmt.Errorf("unknown service %q", svc)
			}
			rows = append(rows, row{service: svc, id: compose.ContainerID(project, svc)})
		}
	}

	detail, err := listContainerDetails(socket)
	if err != nil {
		return err
	}
	byID := make(map[string]containerDetail, len(detail))
	for _, it := range detail {
		byID[it.ID] = it
	}

	if opts.quiet {
		for _, r := range rows {
			it, ok := byID[r.id]
			if !ok {
				continue
			}
			if it.ShortID != "" && !opts.noTrunc {
				fmt.Println(it.ShortID)
			} else {
				fmt.Println(it.ID)
			}
		}
		return nil
	}

	fmt.Println("SERVICE            CONTAINER ID   IMAGE                          STATUS    PORTS                     IP")
	for _, r := range rows {
		it, ok := byID[r.id]
		status, image, ports, ip, cid := "absent", "-", "-", "-", "-"
		if ok {
			status = it.Status
			if status == "" {
				status = "unknown"
			}
			image = it.Image
			ports = it.Ports
			if ports == "" {
				ports = "-"
			}
			ip = it.IP
			cid = it.ShortID
			if opts.noTrunc || cid == "" {
				cid = it.ID
			}
			if !opts.noTrunc && cid == it.ID && len(cid) > 12 {
				cid = cid[:12]
			}
		}
		svc := r.service
		if len(svc) > 18 {
			svc = svc[:15] + "..."
		}
		if len(image) > 30 {
			image = image[:27] + "..."
		}
		if len(ports) > 24 {
			ports = ports[:21] + "..."
		}
		fmt.Printf("%-18s %-14s %-30s %-9s %-25s %s\n", svc, cid, image, status, ports, ip)
	}
	return nil
}

type containerDetail struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Image   string `json:"image"`
	IP      string `json:"ip"`
	Ports   string `json:"ports"`
	Status  string `json:"status"`
}

func listContainerDetails(socket string) ([]containerDetail, error) {
	resp, err := httpClient(socket).Get("http://unix/v1/containers?detail=1")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ps: %w", daemonError(resp.StatusCode, body))
	}
	var out struct {
		Items []containerDetail `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("ps: decode: %w", err)
	}
	return out.Items, nil
}

func doComposeStats(socket string, args []string) error {
	opts, err := parseComposeStatsArgs(args)
	if err != nil {
		return err
	}
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	st, err := compose.ParseFile(abs)
	if err != nil {
		return fmt.Errorf("compose: %w", err)
	}
	project := strings.TrimSpace(opts.project)
	if project == "" {
		project = compose.ProjectName(st, abs)
	}
	var ids []string
	if len(opts.services) == 0 {
		ids = compose.OrderedContainerIDs(st, project)
	} else {
		known := make(map[string]struct{}, len(st.Services))
		for name := range st.Services {
			known[name] = struct{}{}
		}
		for _, svc := range opts.services {
			if _, ok := known[svc]; !ok {
				return fmt.Errorf("unknown service %q", svc)
			}
			ids = append(ids, compose.ContainerID(project, svc))
		}
	}
	// Only ask the daemon for containers that are actually running; missing
	// stack members are skipped (docker compose stats does the same).
	running, err := filterRunningComposeIDs(socket, ids)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		printStatsHeader()
		fmt.Fprintf(os.Stderr, "no containers for project %q\n", project)
		return nil
	}
	return streamStats(socket, running, opts.noStream, opts.noTrunc, project)
}

func filterRunningComposeIDs(socket string, want []string) ([]string, error) {
	resp, err := httpClient(socket).Get("http://unix/v1/containers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stats: %w", daemonError(resp.StatusCode, body))
	}
	var out struct {
		Containers []string `json:"containers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("stats: decode: %w", err)
	}
	alive := make(map[string]struct{}, len(out.Containers))
	for _, id := range out.Containers {
		alive[id] = struct{}{}
	}
	var ids []string
	for _, id := range want {
		if _, ok := alive[id]; ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func doComposeUp(socket string, opts composeCLI) error {
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	if st, err := compose.ParseFile(abs); err == nil {
		warnComposeSecurity(st)
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

// warnComposeSecurity prints stderr warnings for compose fields that weaken
// isolation (host network, privileged, host sockets / docker.sock binds).
func warnComposeSecurity(st *compose.Stack) {
	if st == nil {
		return
	}
	names := make([]string, 0, len(st.Services))
	for n := range st.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var hostNet, priv, hostSock []string
	for _, n := range names {
		svc := st.Services[n]
		if compose.ServiceUsesHostNetwork(st, svc) {
			hostNet = append(hostNet, n)
		}
		if svc.Privileged {
			priv = append(priv, n)
		}
		for _, v := range svc.Volumes {
			src, _, _ := strings.Cut(v, ":")
			src = strings.TrimSpace(src)
			if src == "/var/run/docker.sock" || src == "/run/docker.sock" ||
				src == "/run/nyxd/nyxd.sock" || strings.HasSuffix(src, "/nyxd.sock") {
				hostSock = append(hostSock, n)
				break
			}
		}
	}
	if len(hostNet) > 0 {
		fmt.Fprintf(os.Stderr, "warning: services share the host network (%s): no network namespace isolation — see docs/security.md\n",
			strings.Join(hostNet, ", "))
	}
	if len(priv) > 0 {
		fmt.Fprintf(os.Stderr, "warning: privileged services (%s): nearly host-equivalent — see docs/security.md\n",
			strings.Join(priv, ", "))
	}
	if len(hostSock) > 0 {
		fmt.Fprintf(os.Stderr, "warning: services mount a host control socket (%s): that is root-equivalent for the daemon — see docs/security.md\n",
			strings.Join(hostSock, ", "))
	}
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
