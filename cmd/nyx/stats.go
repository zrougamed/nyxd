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

	printHeader := true
	for {
		items, err := fetchStats(ctx, socket, ids)
		if err != nil {
			return err
		}
		if printHeader || !isTTY(os.Stdout) {
			printStatsHeader()
			printHeader = false
		} else {
			// Clear previous table rows (header + N lines) when streaming on a TTY.
			clearStatsScreen(len(items))
			printStatsHeader()
		}
		if len(items) == 0 {
			if projectHint != "" {
				fmt.Fprintf(os.Stderr, "no containers for project %q\n", projectHint)
			}
		}
		for _, it := range items {
			printStatsRow(it, noTrunc)
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

func printStatsHeader() {
	fmt.Println("CONTAINER ID   NAME                     CPU %     MEM USAGE / LIMIT     MEM %     NET I/O           BLOCK I/O         PIDS")
}

func printStatsRow(it statsItem, noTrunc bool) {
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
		fmt.Printf("%-14s %-24s %-9s %-20s %-9s %-17s %-17s %s\n",
			cid, name, "--", "--", "--", "--", "--", "--")
		return
	}
	fmt.Printf("%-14s %-24s %-9s %-20s %-9s %-17s %-17s %d\n",
		cid,
		name,
		fmt.Sprintf("%.2f%%", it.CPUPerc),
		fmt.Sprintf("%s / %s", humanBytes(it.MemUsage), humanBytes(it.MemLimit)),
		fmt.Sprintf("%.2f%%", it.MemPerc),
		fmt.Sprintf("%s / %s", humanBytes(it.NetRx), humanBytes(it.NetTx)),
		fmt.Sprintf("%s / %s", humanBytes(it.BlockRead), humanBytes(it.BlockWrite)),
		it.PIDs,
	)
}

func clearStatsScreen(nRows int) {
	// Move cursor up past previous header+rows and clear downward.
	lines := nRows + 1
	if lines < 1 {
		lines = 1
	}
	fmt.Printf("\033[%dA\033[J", lines)
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
