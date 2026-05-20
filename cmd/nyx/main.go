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
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

func defaultSocket() string {
	if v := os.Getenv("NYXD_SOCKET"); v != "" {
		return v
	}
	return "/run/nyxd/nyxd.sock"
}

// mimeExecStreamV1 must match internal/control for streaming stdin (nyx exec -i).
// First JSON line may include "tty": true for PTY allocation.
const mimeExecStreamV1 = "application/x-nyxd-exec+v1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errExecReported) {
			fmt.Fprintf(os.Stderr, "nyx: %v\n", err)
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

commands:
  ping
      check that nyxd is reachable
  version
      print daemon version / build info
  pull [--json] [--username USER] [--password PASS] <ref>
      pull an image into the local store; default shows progress, --json streams raw events
  run [flags] <image> [-- <argv...>]
      start a container. default: stay attached and stream logs until the workload exits.
      -d / --detach: start in the background and print the container id.
      flags:
        --name <id>                 container id (default: auto-generated)
        -p, --publish HOST:PORT[/tcp|udp]   port mapping (repeatable, e.g. -p 8080:80)
        -e, --env KEY=VAL           environment (repeatable)
        --hostname <h> | -h <h>     hostname in container (-h here is hostname, not help)
        --restart <policy>          always | on-failure | unless-stopped | never
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

  compose up|stop|down [-f|--file PATH] [--project NAME] [-v|--volumes]
      compose up: start stack (default file: first of nyx-compose.yaml, docker-compose.yml,
      compose.yaml, podman-compose.yml, plus .yaml/.yml variants, in cwd).
      compose stop: SIGTERM services in reverse dependency order.
      compose down: remove stack containers; -v deletes declared named volume dirs when unreferenced.

environment:
  NYXD_SOCKET   default control socket path
`, defaultSocket())
}

func httpClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
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
		return fmt.Errorf("ping: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func doVersion(socket string) error {
	c := httpClient(socket)
	resp, err := c.Get("http://unix/v1/version")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("version: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// formatRunFailure turns HTTP error responses into a single readable line (no redundant "502 Bad Gateway" prefix).
func formatRunFailure(code int, body string) string {
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "\n", " ")
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

func doRun(socket string, args []string) error {
	o, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	body := map[string]any{"image": o.image}
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
	// Foreground: stream pull progress (NDJSON). Detach uses a compact JSON response
	// unless --json (same as pre-stream behavior) to avoid extra encode/parse cost.
	if !o.jsonOut && !o.detach {
		body["stream"] = true
	}
	raw, _ := json.Marshal(body)

	c := httpClient(socket)
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/containers/run", bytes.NewReader(raw))
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-sigCh:
		logCancel()
		killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := doKillWithContext(killCtx, socket, out.ID); err != nil {
			if !httpStatusNotFound(err) {
				fmt.Fprintf(os.Stderr, "nyx: kill: %v\n", err)
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "SIGKILL sent to %s\n", out.ID)
		}
		return nil
	case err := <-logDone:
		logCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "nyx: log stream: %v\n", err)
			return err
		}
		return nil
	}
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	return nil
}

// httpStatusNotFound reports whether err is an HTTP 404 from the control API.
func httpStatusNotFound(err error) bool {
	if err == nil {
		return false
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
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
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
	if err == nil || len(argv) == 0 || containerID == "" {
		return err
	}
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
		return fmt.Errorf("exec: %s: %s", resp.Status, bytes.TrimSpace(b))
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
