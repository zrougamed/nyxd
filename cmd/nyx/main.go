// nyx — CLI client for nyxd's Unix-socket HTTP control API.
package main

import (
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
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

const systemSocket = "/run/nyxd/nyxd.sock"

// defaultSocket picks NYXD_SOCKET, else the nyx machine (WSL distro on
// Windows, the VM's socket on macOS once created), else (for non-root users)
// the user's own rootless daemon at $XDG_RUNTIME_DIR/nyxd/nyxd.sock when it
// exists, else the system daemon.
func defaultSocket() string {
	if v := os.Getenv("NYXD_SOCKET"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return wslScheme + "nyx" // see nyx machine
	}
	if runtime.GOOS == "darwin" {
		if p := darwinMachineSocket(); p != "" {
			return p // the nyx VM (nyx machine)
		}
	}
	if os.Geteuid() != 0 {
		if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
			p := filepath.Join(d, "nyxd", "nyxd.sock")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return systemSocket
}

// mimeExecStreamV1 must match internal/control for streaming stdin (nyx exec -i).
// First JSON line may include "tty": true for PTY allocation.
const mimeExecStreamV1 = "application/x-nyxd-exec+v1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errExecReported) {
			fmt.Fprintf(os.Stderr, "nyx: %v\n", explainDialError(err))
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	socket := defaultSocket()
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case args[0] == "-socket" && len(args) > 1:
			socket = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "-socket="):
			socket = strings.TrimPrefix(args[0], "-socket=")
			args = args[1:]
		case args[0] == "-h" || args[0] == "--help":
			usage()
			return nil
		case args[0] == "-v" || args[0] == "--version":
			fmt.Printf("nyx %s\n", version)
			return nil
		default:
			return fmt.Errorf("unknown flag %q", args[0])
		}
	}
	if len(args) < 1 {
		usage()
		return fmt.Errorf("no command specified; try nyx --help")
	}

	switch args[0] {
	case "ping":
		return doPing(socket)
	case "version":
		return doVersion(socket)
	case "pull":
		ref, jsonOut, u, p, err := parsePullArgs(args[1:])
		if err != nil {
			return err
		}
		return doPull(socket, ref, jsonOut, u, p)
	case "run":
		return doRun(socket, args[1:])
	case "ps":
		return doPS(socket, args[1:])
	case "rm":
		return doRM(socket, args[1:])
	case "logs":
		return doLogs(socket, args[1:])
	case "image":
		return doImage(socket, args[1:])
	case "container":
		return doContainer(socket, args[1:])
	case "stop":
		if len(args) < 2 {
			return fmt.Errorf("usage: nyx stop <id> [<id>...]")
		}
		return doStopMany(socket, args[1:])
	case "exec":
		return doExec(socket, args[1:])
	case "compose":
		return doCompose(socket, args[1:])
	case "build":
		return doBuild(socket, args[1:])
	case "machine":
		return doMachine(args[1:])
	case "tag":
		return doTag(socket, args[1:])
	case "push":
		return doPush(socket, args[1:])
	case "login":
		return doLogin(args[1:])
	case "logout":
		return doLogout(args[1:])
	case "save":
		return doSave(socket, args[1:])
	case "load":
		return doLoad(socket, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `nyx — CLI for nyxd (container daemon over a Unix socket)

usage:
  nyx [global-options] <command> [args ...]

global-options (before <command>):
  -socket PATH | -socket=PATH   control socket (default when NYXD_SOCKET unset: %s)
  -h, --help                   show this help (must come before the command name)
  -v, --version                print the nyx version (nyx version also asks the daemon)

commands:
  ping
      check that nyxd is reachable
  machine <init|start|stop|rm|status|logs|autostart|run> [--name NAME]
      manage where nyxd runs when the host is not Linux: the WSL2 distro on Windows,
      the nyx VM (vfkit) on macOS. No admin rights needed. nyx machine (no args) for details
  version
      print daemon version / build info
  login [-u USER] [-p PASS | --password-stdin] [--authfile PATH] [REGISTRY]
      check credentials against a registry (default docker.io) and save them for nyx
  logout [--authfile PATH] [REGISTRY]
      remove credentials saved by nyx login
  pull [--json] [--username USER] [--password PASS] <ref>
      pull an image into the local store; default shows progress, --json streams raw events.
      pull, run, push, build and compose up use your registry logins (see "registry logins")
  run [flags] <image> [command [args...]]   (a "--" before the command is optional)
      start a container. default: stay attached and stream logs until the workload exits.
      -d / --detach: start in the background and print the container id.
      flags:
        --name <id>                 container id (default: auto-generated)
        -p, --publish HOST:PORT[/tcp|udp]   port mapping (repeatable, e.g. -p 8080:80)
        -e, --env KEY=VAL           environment (repeatable)
        --hostname <h> | -h <h>     hostname in container (-h here is hostname, not help)
        --restart <policy>          always | on-failure | unless-stopped | never (default)
        --rm                        remove container logs/state when it exits (like docker --rm)
        -i, --interactive           keep stdin open; attach stdin in the foreground
        -t, --tty                   allocate a PTY for the container init
        -it                         shorthand for -i -t (interactive shell)
        --print-id                  when attached, also print id on stderr
        --json                      machine-readable pull/run output
  ps [-q] [--no-trunc]
      list containers
  logs [-f] [--tail N|-n N] <id>
      show container logs; -f follows new lines
  stop <id> [<id>...]
      send graceful stop (SIGTERM / image stop signal, then wait)
  rm <id> [<id>...]
      remove container(s) and their supervisor state
  build [-t TAG]... [-f Dockerfile] [--build-arg K=V]... [--target STAGE] [--no-cache] [-q] <context-dir>
      build an image from a Dockerfile (no Docker/BuildKit; RUN runs in the container sandbox).
      the context honours .dockerignore; -q prints only the image id
  tag <source> <target>
      give a local image another reference (e.g. registry.example.com/team/app:1.0)
  push [--username USER] [--password PASS] <ref>
      upload a local image to its registry; localhost registries use http
  save [-o FILE] <ref> [<ref>...]
      write images to a tar archive (OCI layout + docker manifest.json; loads in docker/podman)
  load [-i FILE]
      import images from an archive made by nyx, docker or podman save (stdin without -i)
  image ls
      list image refs present in the local store
  image rm <ref> [<ref>...]
      remove local image metadata (layers may be pruned if unreferenced)
  image prune [--dry-run|-n]
      delete pulled images not referenced by any running container
  container <ls|list|rm|logs> ...
      aliases: same as ps, rm, logs with different word order
  exec [-i] [-t|-it] <id> [--] <argv...>
      run a command in a running container; -i streams stdin from this terminal.
      -t allocates a PTY (shell prompt, line editing); combine as -it for an interactive shell.

  compose up|stop|down [-f|--file PATH] [--project NAME] [-v|--volumes] [--build]
      compose up: start stack (default file: first of nyx-compose.yaml, docker-compose.yml,
      compose.yaml, podman-compose.yml, plus .yaml/.yml variants, in cwd).
      services with build: are built first when their image is missing (--build: always).
      compose stop: SIGTERM services in reverse dependency order.
      compose down: remove stack containers; -v deletes declared named volume dirs when unreferenced.

registry logins (first match wins; docker login and podman login work as they are):
  1. --username/--password flags, then NYX_REGISTRY_USERNAME / NYX_REGISTRY_PASSWORD
  2. nyx:    $NYX_AUTH_FILE, else ~/.config/nyx/auth.json (nyx login)
  3. podman: $REGISTRY_AUTH_FILE, $XDG_RUNTIME_DIR/containers/auth.json, ~/.config/containers/auth.json
  4. docker: $DOCKER_CONFIG/config.json, else ~/.docker/config.json (credHelpers / credsStore
             run docker-credential-<name>, as docker does)

environment:
  NYX_REGISTRY_USERNAME, NYX_REGISTRY_PASSWORD
                credentials for every registry (pull, run, push)
  NYXD_SOCKET   default control socket path (else $XDG_RUNTIME_DIR/nyxd/nyxd.sock
                for a rootless daemon when present, else /run/nyxd/nyxd.sock)
`, defaultSocket())
}

func httpClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialEndpoint(ctx, socket)
			},
		},
		Timeout: 30 * time.Minute,
	}
}

func doPing(socket string) error {
	c := httpClient(socket)
	resp, err := c.Get("http://unix/v1/ping")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ping: %w", daemonError(resp.StatusCode, b))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func doVersion(socket string) error {
	fmt.Printf("nyx %s\n", version)
	c := httpClient(socket)
	resp, err := c.Get("http://unix/v1/version")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("version: %w", daemonError(resp.StatusCode, b))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// explainDialError replaces "dial unix …: no such file or directory" and friends
// with what to do about it: start nyxd, or get access to its socket.
func explainDialError(err error) error {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" || op.Addr == nil {
		return err
	}
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		if runtime.GOOS == "darwin" && darwinMachineSocket() != "" {
			return fmt.Errorf("cannot reach nyxd at %s: is the nyx machine running? (nyx machine start)", op.Addr)
		}
		return fmt.Errorf("cannot reach nyxd at %s: is it running? (e.g. sudo systemctl start nyxd; another socket: -socket or NYXD_SOCKET)", op.Addr)
	case errors.Is(err, syscall.EACCES):
		return fmt.Errorf("permission denied on %s: join the nyx group (sudo usermod -aG nyx \"$USER\", then log in again) or use sudo", op.Addr)
	}
	return err
}

// formatRunFailure turns HTTP error responses into readable text (no redundant
// "502 Bad Gateway" prefix). Line breaks are kept: hints come on their own line.
func formatRunFailure(code int, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "(empty response body)"
	}
	switch code {
	case http.StatusBadGateway:
		return "could not pull or resolve image — " + body
	case http.StatusServiceUnavailable:
		return "daemon unavailable — " + body
	case http.StatusBadRequest:
		return body
	default:
		return fmt.Sprintf("unexpected HTTP %d — %s", code, body)
	}
}

const mimeRunStreamV1 = "application/x-nyxd-run+v1"

func doRun(socket string, args []string) error {
	o, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if o.rm {
		switch strings.ToLower(strings.TrimSpace(o.restart)) {
		case "always", "unless-stopped", "unless_stopped":
			return fmt.Errorf("--rm cannot be used with --restart %s", o.restart)
		}
	}
	// Foreground -i/-t: attach stdio to the container init (like docker run -it).
	if !o.detach && (o.stdin || o.tty) {
		return doRunAttached(socket, o)
	}

	body := map[string]any{"image": o.image}
	// Used only if the daemon has to pull the image.
	if u, p := registryCredentials(o.image); u != "" {
		body["registry_username"], body["registry_password"] = u, p
	}
	if o.name != "" {
		body["id"] = o.name
	}
	if len(o.cmdArgs) > 0 {
		body["args"] = o.cmdArgs
	}
	if len(o.env) > 0 {
		body["env"] = o.env
	}
	if o.hostname != "" {
		body["hostname"] = o.hostname
	}
	if o.restart != "" {
		body["restart"] = o.restart
	}
	if len(o.publish) > 0 {
		body["publish"] = o.publish
	}
	if o.rm {
		body["auto_remove"] = true
	}
	if o.tty {
		body["tty"] = true
	}
	if o.stdin {
		body["stdin"] = true
	}
	// Foreground: stream pull progress (NDJSON). Detach uses a compact JSON response
	// unless --json (same as pre-stream behavior) to avoid extra encode/parse cost.
	if !o.jsonOut && !o.detach {
		body["stream"] = true
	}
	raw, _ := json.Marshal(body)

	// Foreground: catch Ctrl+C from the start. One that arrives while the
	// image is pulled or the container created kills the container as soon
	// as its id is known; a second one aborts without waiting.
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	sigCh := make(chan os.Signal, 1)
	interrupted := make(chan struct{})
	started := make(chan struct{})
	stopWatch := sync.OnceFunc(func() { close(started) })
	defer stopWatch()
	if !o.detach {
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		go func() {
			select {
			case <-sigCh:
				close(interrupted)
				fmt.Fprintln(os.Stderr, "nyx: interrupted; the container is killed once it starts (interrupt again to abort)")
				select {
				case <-sigCh:
					cancelReq()
				case <-started:
				}
			case <-started:
			}
		}()
	}

	c := httpClient(socket)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://unix/v1/containers/run", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("run: %s", formatRunFailure(resp.StatusCode, string(bytes.TrimSpace(b))))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	var out struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Image string `json:"image"`
	}
	var rawJSON []byte
	if !o.jsonOut && strings.Contains(ct, "ndjson") {
		id, img, err := consumeRunPullStream(resp.Body, o.image)
		if err != nil {
			return err
		}
		out.OK, out.ID, out.Image = true, id, img
	} else {
		var err error
		rawJSON, err = io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(rawJSON, &out); err != nil || !out.OK || out.ID == "" {
			return fmt.Errorf("run: bad response: %s", bytes.TrimSpace(rawJSON))
		}
	}

	stopWatch()

	if o.detach {
		if o.jsonOut {
			os.Stdout.Write(rawJSON)
			if len(rawJSON) > 0 && rawJSON[len(rawJSON)-1] != '\n' {
				fmt.Println()
			}
		} else {
			fmt.Println(out.ID)
		}
		return nil
	}

	// Foreground: container output on stdout; optional id on stderr (--print-id). Ctrl+C sends SIGKILL.
	if o.printID {
		fmt.Fprintf(os.Stderr, "%s\n", out.ID)
	}

	select {
	case <-interrupted: // Ctrl+C while the container was being started
		return killRunContainer(socket, out.ID)
	default:
	}

	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()
	logDone := make(chan error, 1)
	go func() {
		err := streamContainerLogs(logCtx, socket, out.ID, os.Stdout)
		if err != nil && errors.Is(err, context.Canceled) {
			err = nil
		}
		logDone <- err
	}()

	select {
	case <-interrupted:
	case <-sigCh:
	case err := <-logDone:
		if err != nil {
			fmt.Fprintf(os.Stderr, "nyx: log stream: %v\n", err)
			return err
		}
		return nil
	}
	logCancel()
	return killRunContainer(socket, out.ID)
}

// doRunAttached is foreground `nyx run -i` / `-t` / `-it`: stdio attached to init.
func doRunAttached(socket string, o runOpts) error {
	body := map[string]any{"image": o.image, "stdin": o.stdin, "tty": o.tty}
	if u, p := registryCredentials(o.image); u != "" {
		body["registry_username"], body["registry_password"] = u, p
	}
	if o.name != "" {
		body["id"] = o.name
	}
	if len(o.cmdArgs) > 0 {
		body["args"] = o.cmdArgs
	}
	if len(o.env) > 0 {
		body["env"] = o.env
	}
	if o.hostname != "" {
		body["hostname"] = o.hostname
	}
	if o.restart != "" {
		body["restart"] = o.restart
	}
	if len(o.publish) > 0 {
		body["publish"] = o.publish
	}
	if o.rm {
		body["auto_remove"] = true
	}
	if o.tty && term.IsTerminal(int(os.Stdin.Fd())) {
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err == nil && cols > 0 && rows > 0 {
			body["cols"] = cols
			body["rows"] = rows
		}
	}
	hdr, err := json.Marshal(body)
	if err != nil {
		return err
	}

	var restoreTTY func()
	if o.tty && o.stdin && term.IsTerminal(int(os.Stdin.Fd())) {
		old, terr := term.MakeRaw(int(os.Stdin.Fd()))
		if terr == nil {
			fd := int(os.Stdin.Fd())
			restoreTTY = func() {
				_ = term.Restore(fd, old)
				nudgeParentShellRedraw()
				_, _ = fmt.Fprint(os.Stderr, "\x1b[?25h\x1b[?2004l")
				_ = os.Stderr.Sync()
			}
		}
	}
	if restoreTTY != nil {
		defer restoreTTY()
	}

	pr, pw := io.Pipe()
	defer func() {
		unblockStdinRead()
		clearStdinReadDeadline()
	}()
	go func() {
		if _, werr := pw.Write(append(hdr, '\n')); werr != nil {
			_ = pw.CloseWithError(werr)
			return
		}
		if o.stdin {
			_, _ = io.Copy(pw, os.Stdin)
		}
		_ = pw.Close()
	}()

	c := httpClient(socket)
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/containers/run", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mimeRunStreamV1)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("run: %s", formatRunFailure(resp.StatusCode, string(bytes.TrimSpace(b))))
	}
	if o.printID {
		if id := resp.Header.Get("X-Nyxd-Container-Id"); id != "" {
			fmt.Fprintf(os.Stderr, "%s\n", id)
		}
	}
	tailRing := &execTailRing{}
	_, copyErr := io.Copy(io.MultiWriter(tailRing, os.Stdout), resp.Body)
	tail := tailRing.Bytes()
	if ferr := runFailureFromTail(tail); ferr != nil {
		if strings.Contains(ferr.Error(), "exit status 130") || strings.Contains(ferr.Error(), "signal: interrupt") {
			return nil
		}
		return maybeRunShellHint(ferr, o.cmdArgs, o.image, tail)
	}
	if copyErr != nil {
		return maybeRunShellHint(copyErr, o.cmdArgs, o.image, tail)
	}
	return maybeRunShellHint(nil, o.cmdArgs, o.image, tail)
}

// maybeRunShellHint prints a short tip when init is bash but the image has none (e.g. alpine).
func maybeRunShellHint(err error, argv []string, image string, tail []byte) error {
	if len(argv) < 1 {
		return err
	}
	base := strings.ToLower(filepath.Base(strings.TrimSpace(argv[0])))
	if base != "bash" {
		return err
	}
	low := strings.ToLower(string(tail))
	if err != nil {
		low += "\n" + strings.ToLower(err.Error())
	}
	if !strings.Contains(low, "executable file") &&
		!strings.Contains(low, "not found in $path") &&
		!strings.Contains(low, "no such file") {
		return err
	}
	img := strings.TrimSpace(image)
	if img == "" {
		img = "<image>"
	}
	fmt.Fprintf(os.Stderr, "nyx: no bash in this image; try: nyx run --rm -it %s sh\n", img)
	return errExecReported
}

// killRunContainer SIGKILLs the container of an interrupted foreground run.
func killRunContainer(socket, id string) error {
	killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := doKillWithContext(killCtx, socket, id); err != nil {
		if !httpStatusNotFound(err) {
			fmt.Fprintf(os.Stderr, "nyx: kill: %v\n", err)
			return err
		}
		return nil
	}
	fmt.Fprintf(os.Stderr, "SIGKILL sent to %s\n", id)
	return nil
}

func doStop(socket, id string) error {
	return doStopWithContext(context.Background(), socket, id)
}

func doKillWithContext(ctx context.Context, socket, id string) error {
	c := httpClient(socket)
	raw, _ := json.Marshal(map[string]string{"signal": "KILL"})
	u := fmt.Sprintf("http://unix/v1/containers/%s/kill", url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return notFoundError{daemonError(resp.StatusCode, body)}
	}
	if resp.StatusCode != http.StatusOK {
		return daemonError(resp.StatusCode, body)
	}
	return nil
}

// notFoundError is a 404 from the control API. daemonError drops the status
// line, so the message alone no longer says it was a 404.
type notFoundError struct{ error }

func (e notFoundError) Unwrap() error { return e.error }

// httpStatusNotFound reports whether err is an HTTP 404 from the control API.
func httpStatusNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf notFoundError
	if errors.As(err, &nf) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "404") && strings.Contains(strings.ToLower(msg), "not found")
}

func doStopWithContext(ctx context.Context, socket, id string) error {
	c := httpClient(socket)
	u := fmt.Sprintf("http://unix/v1/containers/%s/stop", url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return daemonError(resp.StatusCode, body)
	}
	return nil
}

func execShellStdinHint(argv []string) bool {
	if len(argv) < 1 {
		return false
	}
	switch filepath.Base(strings.TrimSpace(argv[0])) {
	case "bash", "sh", "ash", "dash", "ksh", "zsh", "csh", "tcsh":
		return true
	default:
		return false
	}
}

// unblockStdinRead forces any blocked Read on stdin to return so the stdin→exec pipe
// goroutine can exit after the HTTP response body has finished.
func unblockStdinRead() {
	if f, ok := any(os.Stdin).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = f.SetReadDeadline(time.Now().Add(-time.Second))
	}
}

func clearStdinReadDeadline() {
	if f, ok := any(os.Stdin).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = f.SetReadDeadline(time.Time{})
	}
}

func execErrIsBenignUserInterrupt(err error, opts execCLIOptions) bool {
	if err == nil || !opts.WantTTY {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "exit status 130") ||
		strings.Contains(s, "signal: interrupt")
}

// maybeExecShellHint appends a short hint when the image clearly lacks the requested shell.
// tail is the raw exec byte stream (PTY + nyxd trailer); crun often prints the real reason
// there while failureFromTail only captures the short "nyxd: exec: crun exec: exit status 255" line.
func maybeExecShellHint(err error, argv []string, containerID string, tail []byte) error {
	if err == nil || len(argv) == 0 {
		return err
	}
	_ = containerID
	base := strings.ToLower(filepath.Base(strings.TrimSpace(argv[0])))
	if base != "bash" {
		return err
	}
	low := strings.ToLower(err.Error() + "\n" + string(tail))
	if !strings.Contains(low, "executable file") &&
		!strings.Contains(low, "not found in $path") &&
		!strings.Contains(low, "no such file") {
		return err
	}
	return errExecReported
}

// shellExit127Hint explains 127 after an interactive sh session when the PTY transcript
// shows a prior "not found" — bare `exit` reuses $? (ash/busybox/bash).
func shellExit127Hint(err error, argv []string, tail []byte, wantTTY bool) error {
	if errors.Is(err, errExecReported) {
		return err
	}
	if err == nil || !wantTTY || len(argv) < 1 {
		return err
	}
	if !strings.Contains(strings.ToLower(err.Error()), "exit status 127") {
		return err
	}
	switch strings.ToLower(filepath.Base(strings.TrimSpace(argv[0]))) {
	case "sh", "ash", "dash":
	default:
		return err
	}
	if !strings.Contains(strings.ToLower(string(tail)), "not found") {
		return err
	}
	return fmt.Errorf("%w\nnyx: bare `exit` keeps the last command's status (127 = not found). Use `exit 0` to leave successfully.", err)
}

func execRequestJSON(argv []string, wantTTY bool) ([]byte, error) {
	m := map[string]any{"argv": argv, "tty": wantTTY}
	if wantTTY && term.IsTerminal(int(os.Stdin.Fd())) {
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err == nil && cols > 0 && rows > 0 {
			m["cols"] = cols
			m["rows"] = rows
		}
	}
	return json.Marshal(m)
}

func doExec(socket string, args []string) error {
	opts, err := parseExecArgs(args)
	if err != nil {
		return err
	}

	var restoreTTY func()
	if opts.WantTTY && opts.AttachStdin && term.IsTerminal(int(os.Stdin.Fd())) {
		old, terr := term.MakeRaw(int(os.Stdin.Fd()))
		if terr == nil {
			fd := int(os.Stdin.Fd())
			restoreTTY = func() {
				_ = term.Restore(fd, old)
				nudgeParentShellRedraw()
				// Show cursor, disable bracketed paste; avoid extra blank lines before the shell prompt.
				_, _ = fmt.Fprint(os.Stderr, "\x1b[?25h\x1b[?2004l")
				_ = os.Stderr.Sync()
			}
		}
	}

	c := httpClient(socket)
	u := fmt.Sprintf("http://unix/v1/containers/%s/exec", url.PathEscape(opts.ID))

	var req *http.Request
	if opts.AttachStdin {
		hdr, err := execRequestJSON(opts.Argv, opts.WantTTY)
		if err != nil {
			return err
		}
		pr, pw := io.Pipe()
		defer func() {
			unblockStdinRead()
			clearStdinReadDeadline()
		}()
		go func() {
			if _, werr := pw.Write(append(hdr, '\n')); werr != nil {
				_ = pw.CloseWithError(werr)
				return
			}
			_, _ = io.Copy(pw, os.Stdin)
			_ = pw.Close()
		}()
		req, err = http.NewRequest(http.MethodPost, u, pr)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mimeExecStreamV1)
	} else {
		body, err := execRequestJSON(opts.Argv, opts.WantTTY)
		if err != nil {
			return err
		}
		req, err = http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
	}

	// Defer after stdin pipe: LIFO runs this before unblocking stdin so the terminal is
	// sane again before the stdin copy goroutine wakes up.
	if restoreTTY != nil {
		defer restoreTTY()
	}

	if opts.AttachStdin && execShellStdinHint(opts.Argv) && !opts.WantTTY {
		fmt.Fprintf(os.Stderr, "nyx: note: exec has no PTY, so shells show no prompt. Type a command and press Enter; use exit or Ctrl+D to finish.\n")
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exec: %w", daemonError(resp.StatusCode, b))
	}
	tailRing := &execTailRing{}
	// Line-based filters break interactive TTYs: raw mode echoes single bytes
	// without newlines, so output would stay buffered until Enter.
	display := io.Writer(os.Stdout)
	var stripNyxd *nyxdExecTrailerFilter
	var bashFl *bashExecCleanFilter
	if !opts.WantTTY {
		stripNyxd = newNyxdExecTrailerFilter(os.Stdout)
		display = stripNyxd
		if len(opts.Argv) > 0 && strings.EqualFold(filepath.Base(strings.TrimSpace(opts.Argv[0])), "bash") {
			bashFl = newBashExecCleanFilter(stripNyxd)
			display = bashFl
		}
	}
	mw := io.MultiWriter(tailRing, display)
	_, copyErr := io.Copy(mw, resp.Body)
	if bashFl != nil {
		_ = bashFl.Flush()
	}
	if stripNyxd != nil {
		_ = stripNyxd.Flush()
	}
	tail := tailRing.Bytes()
	if ferr := execFailureFromTail(tail); ferr != nil {
		if execErrIsBenignUserInterrupt(ferr, opts) {
			return nil
		}
		hintErr := maybeExecShellHint(ferr, opts.Argv, opts.ID, tail)
		return shellExit127Hint(hintErr, opts.Argv, tail, opts.WantTTY)
	}
	if execErrIsBenignUserInterrupt(copyErr, opts) {
		return nil
	}
	hintErr := maybeExecShellHint(copyErr, opts.Argv, opts.ID, tail)
	return shellExit127Hint(hintErr, opts.Argv, tail, opts.WantTTY)
}
