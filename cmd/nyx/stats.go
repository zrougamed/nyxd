package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

type statsCLI struct {
	noStream bool
	noTrunc  bool
	ids      []string
}

func parseStatsArgs(args []string) (statsCLI, error) {
	var o statsCLI
	for _, a := range args {
		switch a {
		case "--no-stream":
			o.noStream = true
		case "--no-trunc":
			o.noTrunc = true
		case "-a", "--all":
			// Reserved: nyxd only tracks running/supervised containers today.
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown stats flag %q (supported: --no-stream, --no-trunc, -a)", a)
			}
			o.ids = append(o.ids, a)
		}
	}
	return o, nil
}

func doStats(socket string, args []string) error {
	o, err := parseStatsArgs(args)
	if err != nil {
		return err
	}
	return streamStats(socket, o.ids, o.noStream, o.noTrunc, "")
}

// streamStats polls GET /v1/containers/stats and prints a docker-like table.
// projectHint is only used in the empty-state message (compose vs plain stats).
func streamStats(socket string, ids []string, noStream, noTrunc bool, projectHint string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Streaming on a terminal redraws each frame from the top of the screen
	// (as docker stats does). Moving the cursor up by the number of rows
	// printed miscounts as soon as a row wraps on a narrow terminal or the
	// container count changes, and leaves old headers behind.
	redraw := !noStream && isTTY(os.Stdout)
	hinted := false
	for {
		items, err := fetchStats(ctx, socket, ids)
		if err != nil {
			return err
		}
		var frame strings.Builder
		if redraw {
			frame.WriteString(clearScreen)
		}
		writeStatsTable(&frame, items, noTrunc)
		fmt.Print(frame.String())
		if len(items) == 0 && projectHint != "" && !hinted {
			fmt.Fprintf(os.Stderr, "no containers for project %q\n", projectHint)
			hinted = true
		}
		if noStream {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

type statsItem struct {
	ID         string  `json:"id"`
	ShortID    string  `json:"short_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	CPUPerc    float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	MemPerc    float64 `json:"mem_percent"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	BlockRead  uint64  `json:"block_read"`
	BlockWrite uint64  `json:"block_write"`
	PIDs       uint64  `json:"pids"`
	Err        string  `json:"error"`
}

func fetchStats(ctx context.Context, socket string, ids []string) ([]statsItem, error) {
	q := url.Values{}
	if len(ids) > 0 {
		q.Set("ids", strings.Join(ids, ","))
	}
	// Client already refreshes once a second; keep the daemon sample short.
	q.Set("interval_ms", "200")
	u := "http://unix/v1/containers/stats"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient(socket).Do(req)
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
		Items []statsItem `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("stats: decode: %w", err)
	}
	return out.Items, nil
}

// clearScreen moves the cursor home and clears the screen.
const clearScreen = "\033[H\033[2J"

var statsHeader = []string{"CONTAINER ID", "NAME", "CPU %", "MEM USAGE / LIMIT", "MEM %", "NET I/O", "BLOCK I/O", "PIDS"}

// writeStatsTable writes the header and one row per item, aligned in columns
// at least three spaces apart whatever the values' widths.
func writeStatsTable(w io.Writer, items []statsItem, noTrunc bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, strings.Join(statsHeader, "\t"))
	for _, it := range items {
		fmt.Fprintln(tw, strings.Join(statsRow(it, noTrunc), "\t"))
	}
	_ = tw.Flush()
}

func statsRow(it statsItem, noTrunc bool) []string {
	cid := it.ShortID
	if cid == "" {
		cid = it.ID
		if !noTrunc && len(cid) > 12 {
			cid = cid[:12]
		}
	} else if noTrunc {
		cid = it.ID
	}
	name := it.Name
	if name == "" {
		name = it.ID
	}
	if !noTrunc && len(name) > 24 {
		name = name[:21] + "..."
	}
	if it.Err != "" {
		return []string{cid, name, "--", "--", "--", "--", "--", "--"}
	}
	return []string{
		cid,
		name,
		fmt.Sprintf("%.2f%%", it.CPUPerc),
		humanBytes(it.MemUsage) + " / " + humanBytes(it.MemLimit),
		fmt.Sprintf("%.2f%%", it.MemPerc),
		humanBytes(it.NetRx) + " / " + humanBytes(it.NetTx),
		humanBytes(it.BlockRead) + " / " + humanBytes(it.BlockWrite),
		fmt.Sprintf("%d", it.PIDs),
	}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func humanBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	v := float64(n)
	for _, u := range units {
		v /= 1024
		if v < 1024 {
			if v >= 100 {
				return fmt.Sprintf("%.0f%s", v, u)
			}
			if v >= 10 {
				return fmt.Sprintf("%.1f%s", v, u)
			}
			return fmt.Sprintf("%.2f%s", v, u)
		}
	}
	return fmt.Sprintf("%.1fPiB", v/1024)
}
