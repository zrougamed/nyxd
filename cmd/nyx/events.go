package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

const eventsUsage = `usage: nyx events [--filter KEY=VALUE]... [--since D|TIME] [--json]

Stream container and image events until Ctrl+C.
  --filter KEY=VALUE   type (container, image), event (create, start, die, oom,
                       restart, health_status, remove, pull, build), container
                       (id or name), image; repeat a key for alternatives
  --since D|TIME       replay kept events first (10m, or an RFC 3339 time);
                       nyxd keeps the last 1000 in memory
  --json               one JSON object per line`

type eventJSON struct {
	Time   time.Time         `json:"time"`
	Type   string            `json:"type"`
	Action string            `json:"action"`
	ID     string            `json:"id"`
	Image  string            `json:"image"`
	Attrs  map[string]string `json:"attributes"`
	Msg    string            `json:"message"`
}

func doEvents(socket string, args []string) error {
	q := url.Values{}
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value\n%s", a, eventsUsage)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--filter" || a == "-f":
			v, err := next()
			if err != nil {
				return err
			}
			q.Add("filter", v)
		case strings.HasPrefix(a, "--filter="):
			q.Add("filter", strings.TrimPrefix(a, "--filter="))
		case a == "--since":
			v, err := next()
			if err != nil {
				return err
			}
			q.Set("since", v)
		case strings.HasPrefix(a, "--since="):
			q.Set("since", strings.TrimPrefix(a, "--since="))
		case a == "--json":
			jsonOut = true
		case a == "-h" || a == "--help":
			fmt.Println(eventsUsage)
			return nil
		default:
			return fmt.Errorf("unknown argument %q\n%s", a, eventsUsage)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/events?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := streamClient(socket).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("events: this nyxd is too old (upgrade the daemon)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("events: %w", daemonError(resp.StatusCode, b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e eventJSON
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if jsonOut {
			fmt.Printf("%s\n", line)
		} else {
			fmt.Println(formatEvent(e))
		}
		if e.Type == "events_dropped" {
			return fmt.Errorf("events: %s", e.Msg)
		}
	}
	if ctx.Err() != nil {
		return nil // Ctrl+C
	}
	return sc.Err()
}

// formatEvent is one human line: time, type, action, id, then image and
// attributes in parentheses.
func formatEvent(e eventJSON) string {
	if e.Type == "events_dropped" {
		return e.Time.Format(time.RFC3339) + " events_dropped " + e.Msg
	}
	var extra []string
	if e.Image != "" && e.Type == "container" {
		extra = append(extra, "image="+e.Image)
	}
	keys := make([]string, 0, len(e.Attrs))
	for k := range e.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		extra = append(extra, k+"="+e.Attrs[k])
	}
	s := fmt.Sprintf("%s %s %s %s", e.Time.Local().Format(time.RFC3339), e.Type, e.Action, e.ID)
	if len(extra) > 0 {
		s += " (" + strings.Join(extra, ", ") + ")"
	}
	return s
}
