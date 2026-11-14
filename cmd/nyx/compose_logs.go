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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/logs"
)

type composeLogsCLI struct {
	file       string
	project    string
	follow     bool
	tail       int
	timestamps bool
	noPrefix   bool
	noColor    bool
	services   []string
}

// parseComposeLogsArgs follows docker compose logs: -f is --follow here, so
// the compose file is only --file.
func parseComposeLogsArgs(args []string) (composeLogsCLI, error) {
	c := composeLogsCLI{tail: 200}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--follow":
			c.follow = true
		case a == "-t" || a == "--timestamps":
			c.timestamps = true
		case a == "--no-prefix":
			c.noPrefix = true
		case a == "--no-color":
			c.noColor = true
		case a == "--file" || a == "--project" || a == "--tail" || a == "-n":
			if i+1 >= len(args) {
				return c, fmt.Errorf("%s requires a value", a)
			}
			i++
			if err := c.set(a, args[i]); err != nil {
				return c, err
			}
		case strings.HasPrefix(a, "--file=") || strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "--tail="):
			name, val, _ := strings.Cut(a, "=")
			if err := c.set(name, val); err != nil {
				return c, err
			}
		case strings.HasPrefix(a, "-"):
			return c, fmt.Errorf("unknown flag %q", a)
		default:
			c.services = append(c.services, a)
		}
	}
	return c, nil
}

func (c *composeLogsCLI) set(flag, val string) error {
	switch flag {
	case "--file":
		c.file = val
	case "--project":
		c.project = val
	default: // --tail, -n
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 0 {
			return fmt.Errorf("invalid %s %q", flag, val)
		}
		c.tail = n
	}
	return nil
}

// logColors cycle over services when stdout is a terminal.
var logColors = []string{"36", "33", "32", "35", "34", "91", "96", "93"}

// logPrinter writes log entries from several services to one writer. Lines
// from concurrent followers are written whole, never interleaved mid-line.
type logPrinter struct {
	mu         sync.Mutex
	w          io.Writer
	width      int
	color      map[string]string
	noPrefix   bool
	timestamps bool
}

func newLogPrinter(w io.Writer, services []string, opts composeLogsCLI, colored bool) *logPrinter {
	p := &logPrinter{w: w, noPrefix: opts.noPrefix, timestamps: opts.timestamps}
	for _, s := range services {
		p.width = max(p.width, len(s))
	}
	if colored {
		p.color = make(map[string]string, len(services))
		for i, s := range services {
			p.color[s] = logColors[i%len(logColors)]
		}
	}
	return p
}

func (p *logPrinter) print(service string, e logs.Entry) {
	var b strings.Builder
	if !p.noPrefix {
		prefix := fmt.Sprintf("%-*s |", p.width, service)
		if c := p.color[service]; c != "" {
			prefix = "\x1b[" + c + "m" + prefix + "\x1b[0m"
		}
		b.WriteString(prefix)
		b.WriteByte(' ')
	}
	if p.timestamps {
		b.WriteString(e.Time.UTC().Format(time.RFC3339Nano))
		b.WriteByte(' ')
	}
	b.WriteString(strings.TrimSuffix(e.Log, "\n"))
	b.WriteByte('\n')
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = io.WriteString(p.w, b.String())
}

// fetchServiceLogs reads the JSON log lines of one container and calls fn for
// each. It returns false when the container has no log (never started).
// Lines written before start are skipped when opts.tail is 0.
func fetchServiceLogs(ctx context.Context, socket, id string, opts composeLogsCLI, start time.Time, fn func(logs.Entry)) (bool, error) {
	q := url.Values{}
	q.Set("plain", "0")
	if opts.follow {
		q.Set("follow", "1")
	}
	// The daemon reads tail=0 as "default", so --tail 0 asks for one line and
	// drops it below if it is old.
	q.Set("tail", strconv.Itoa(max(opts.tail, 1)))
	u := fmt.Sprintf("http://unix/v1/containers/%s/logs?%s", url.PathEscape(id), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return false, daemonError(resp.StatusCode, b)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		var e logs.Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			e = logs.Entry{Log: sc.Text()}
		}
		if first && opts.tail == 0 && e.Time.Before(start) {
			first = false
			continue
		}
		first = false
		fn(e)
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return true, err
	}
	return true, nil
}

// doComposeLogs prints the logs of a stack's services (like docker compose logs).
func doComposeLogs(socket string, args []string) error {
	opts, err := parseComposeLogsArgs(args)
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
	services := opts.services
	if len(services) == 0 {
		services = compose.TopologicalOrder(st)
	}
	for _, s := range services {
		if _, ok := st.Services[s]; !ok {
			return fmt.Errorf("unknown service %q", s)
		}
	}

	colored := !opts.noColor && os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd()))
	p := newLogPrinter(os.Stdout, services, opts, colored)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	start := time.Now()

	type result struct {
		found bool
		err   error
	}
	results := make([]result, len(services))
	var buffered [][]logs.Entry // per service, when not following
	if !opts.follow {
		buffered = make([][]logs.Entry, len(services))
	}
	var wg sync.WaitGroup
	for i, svc := range services {
		wg.Go(func() {
			id := compose.ContainerID(project, svc)
			found, err := fetchServiceLogs(ctx, socket, id, opts, start, func(e logs.Entry) {
				if opts.follow {
					p.print(svc, e)
				} else {
					buffered[i] = append(buffered[i], e)
				}
			})
			results[i] = result{found, err}
		})
	}
	wg.Wait()

	if !opts.follow {
		for _, line := range mergeServiceLogs(services, buffered) {
			p.print(line.service, line.entry)
		}
	}
	found := false
	for i, r := range results {
		if r.err != nil {
			return fmt.Errorf("logs %s: %w", services[i], r.err)
		}
		found = found || r.found
	}
	if !found {
		return fmt.Errorf("no logs for project %q (is the stack up? nyx compose up)", project)
	}
	return nil
}

type serviceLogLine struct {
	service string
	entry   logs.Entry
}

// mergeServiceLogs orders the lines of every service by time; lines with the
// same time keep their order within a service.
func mergeServiceLogs(services []string, perService [][]logs.Entry) []serviceLogLine {
	var out []serviceLogLine
	for i, entries := range perService {
		for _, e := range entries {
			out = append(out, serviceLogLine{services[i], e})
		}
	}
	slices.SortStableFunc(out, func(a, b serviceLogLine) int { return a.entry.Time.Compare(b.entry.Time) })
	return out
}
