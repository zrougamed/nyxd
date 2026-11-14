package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// doStartMany is nyx start: start exited containers again from their saved spec.
func doStartMany(socket string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nyx start <id> [<id>...]")
	}
	for _, id := range args {
		if strings.HasPrefix(id, "-") {
			return fmt.Errorf("unknown flag %q", id)
		}
		if err := postContainerAction(socket, id, "start", nil); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		fmt.Println(id)
	}
	return nil
}

// parseRestartArgs reads [-t SECONDS] <id>...
func parseRestartArgs(args []string) (grace string, ids []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-t" || a == "--time" || a == "--timeout":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s requires a value in seconds", a)
			}
			i++
			grace = args[i]
		case strings.HasPrefix(a, "-t=") || strings.HasPrefix(a, "--time=") || strings.HasPrefix(a, "--timeout="):
			_, grace, _ = strings.Cut(a, "=")
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("unknown flag %q", a)
		default:
			ids = append(ids, a)
		}
	}
	if grace != "" {
		if n, err := strconv.Atoi(grace); err != nil || n < 0 {
			return "", nil, fmt.Errorf("-t: %q is not a number of seconds", grace)
		}
	}
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("usage: nyx restart [-t SECONDS] <id> [<id>...]")
	}
	return grace, ids, nil
}

// doRestartMany is nyx restart: stop (within -t seconds) and start again.
func doRestartMany(socket string, args []string) error {
	grace, ids, err := parseRestartArgs(args)
	if err != nil {
		return err
	}
	q := url.Values{}
	if grace != "" {
		q.Set("t", grace)
	}
	for _, id := range ids {
		if err := postContainerAction(socket, id, "restart", q); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		fmt.Println(id)
	}
	return nil
}

func postContainerAction(socket, id, action string, q url.Values) error {
	u := fmt.Sprintf("http://unix/v1/containers/%s/%s", url.PathEscape(id), action)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	resp, err := httpClient(socket).Post(u, "application/json", nil)
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

// doComposeLifecycle is nyx compose start / restart [SERVICE...].
func doComposeLifecycle(socket, sub string, opts composeCLI) error {
	if opts.removeVolumes || opts.build {
		return fmt.Errorf("compose %s takes no -v/--volumes or --build", sub)
	}
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	if abs, err = daemonPath(socket, abs); err != nil {
		return err
	}
	body := map[string]any{"file": abs}
	if opts.project != "" {
		body["project"] = opts.project
	}
	if len(opts.positional) > 0 {
		body["services"] = opts.positional
	}
	if sub == "start" {
		return postComposeJSON(socket, "http://unix/v1/compose/start", body, "started", "Starting containers…")
	}
	return postComposeJSON(socket, "http://unix/v1/compose/restart", body, "restarted", "Restarting containers…")
}

// formatPSStatus adds the exit code to an exited container's status.
func formatPSStatus(it psItem) string {
	if it.ExitCode != nil {
		return it.Status + " (" + strconv.Itoa(*it.ExitCode) + ")"
	}
	return it.Status
}
