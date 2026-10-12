package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zrougamed/nyxd/internal/image"
	"golang.org/x/term"
)

func parsePullArgs(args []string) (ref string, jsonOut bool, username, password string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--username", "-u":
			if i+1 >= len(args) {
				return "", false, "", "", fmt.Errorf("usage: nyx pull [--json] [--username USER] [--password PASS] <ref>")
			}
			i++
			username = args[i]
		case "--password":
			if i+1 >= len(args) {
				return "", false, "", "", fmt.Errorf("usage: nyx pull [--json] [--username USER] [--password PASS] <ref>")
			}
			i++
			password = args[i]
		case "-h", "--help":
			return "", false, "", "", fmt.Errorf("usage: nyx pull [--json] [--username USER] [--password PASS] <ref>")
		default:
			if strings.HasPrefix(a, "-") {
				return "", false, "", "", fmt.Errorf("unknown flag %q", a)
			}
			if ref != "" {
				return "", false, "", "", fmt.Errorf("unexpected extra argument %q", a)
			}
			ref = a
		}
	}
	if ref == "" {
		return "", false, "", "", fmt.Errorf("usage: nyx pull [--json] [--username USER] [--password PASS] <ref>")
	}
	return ref, jsonOut, username, password, nil
}

func doPull(socket, ref string, jsonOut bool, username, password string) error {
	c := httpClient(socket)
	bodyMap := map[string]any{
		"ref":    ref,
		"stream": !jsonOut,
	}
	if strings.TrimSpace(username) != "" {
		bodyMap["username"] = username
		bodyMap["password"] = password
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/images/pull", bytes.NewReader(body))
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
		return fmt.Errorf("pull: %s: %s", resp.Status, bytes.TrimSpace(b))
	}

	if jsonOut {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		if err != nil {
			return err
		}
		if len(b) > 0 && b[len(b)-1] != '\n' {
			fmt.Println()
		}
		return nil
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "ndjson") {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}

	return renderPullStream(resp.Body, ref)
}

// pullBar is one blob's progress line.
type pullBar struct {
	title     string
	size, cur int64
	done      bool
}

// pullUI draws one progress line per blob. On a terminal the lines are
// redrawn in place; otherwise each blob gets a single line when it finishes.
// All drawing happens on the caller's goroutine under mu.
type pullUI struct {
	mu        sync.Mutex
	out       io.Writer
	tty       bool
	order     []*pullBar
	bars      map[string]*pullBar
	drawn     int // bar lines currently on screen (tty)
	lastDraw  time.Time
	headerRef string
}

func newPullUI() *pullUI {
	return &pullUI{
		out:  os.Stderr,
		tty:  term.IsTerminal(int(os.Stderr.Fd())),
		bars: make(map[string]*pullBar),
	}
}

func (u *pullUI) ensureBar(digest string, size int64, subtitle string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.bars[digest]; ok {
		return
	}
	title := shortDigest(digest)
	if subtitle != "" {
		title = title + "  " + subtitle
	}
	b := &pullBar{title: title, size: size}
	u.bars[digest] = b
	u.order = append(u.order, b)
	u.redraw(true)
}

// progress records current, the absolute byte count for digest.
func (u *pullUI) progress(digest string, current int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if b, ok := u.bars[digest]; ok && current > b.cur {
		b.cur = current
		u.redraw(false)
	}
}

func (u *pullUI) closeDigest(digest string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	b, ok := u.bars[digest]
	if !ok || b.done {
		return
	}
	b.done = true
	if b.size > 0 {
		b.cur = b.size
	}
	if !u.tty {
		fmt.Fprintln(u.out, b.line())
		return
	}
	u.redraw(true)
}

// note prints a line above the progress lines.
func (u *pullUI) note(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.tty && u.drawn > 0 {
		fmt.Fprintf(u.out, "\x1b[%dA\r\x1b[J", u.drawn)
		u.drawn = 0
	}
	fmt.Fprintf(u.out, format, args...)
	if u.tty {
		u.redraw(true)
	}
}

func (u *pullUI) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.tty {
		u.redraw(true)
	}
}

// redraw repaints the bar lines in place, at most ten times a second
// unless forced. Callers hold mu.
func (u *pullUI) redraw(force bool) {
	if !u.tty || (!force && time.Since(u.lastDraw) < 100*time.Millisecond) {
		return
	}
	u.lastDraw = time.Now()
	var sb strings.Builder
	if u.drawn > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", u.drawn)
	}
	for _, b := range u.order {
		sb.WriteString("\r\x1b[2K")
		sb.WriteString(b.line())
		sb.WriteByte('\n')
	}
	u.drawn = len(u.order)
	_, _ = io.WriteString(u.out, sb.String())
}

func (b *pullBar) line() string {
	const width = 30
	if b.size <= 0 {
		if b.done {
			return fmt.Sprintf("  %s  done  %s", b.title, humanSize(b.cur))
		}
		return fmt.Sprintf("  %s  %s", b.title, humanSize(b.cur))
	}
	n := int(min(b.cur, b.size) * width / b.size)
	bar := strings.Repeat("=", n) + strings.Repeat(" ", width-n)
	if !b.done && n < width {
		bar = strings.Repeat("=", n) + ">" + strings.Repeat(" ", width-n-1)
	}
	return fmt.Sprintf("  %s  [%s]  %s / %s", b.title, bar, humanSize(min(b.cur, b.size)), humanSize(b.size))
}

func renderPullStream(r io.Reader, ref string) error {
	ui := newPullUI()
	defer ui.Close()

	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev image.PullEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("pull stream: decode: %w", err)
		}
		switch ev.Phase {
		case "begin":
			ui.headerRef = ev.Ref
			if ev.Ref != "" {
				ui.note("\n→ Pulling %s\n\n", ev.Ref)
			}
		case "manifest":
			// optional context
		case "auth":
		case "meta":
			ui.note("%s\n", ev.Message)
		case "config":
			ui.ensureBar(ev.Digest, ev.Size, "config")
		case "layer":
			if ev.Cached {
				ui.note("  • %s  (already present)\n", shortDigest(ev.Digest))
				continue
			}
			ui.ensureBar(ev.Digest, ev.Size, "layer")
		case "progress":
			ui.progress(ev.Digest, ev.Current)
		case "layer_done":
			ui.closeDigest(ev.Digest)
		case "done":
			printPullSummary(os.Stdout, &ev)
			return nil
		case "error":
			if ev.Message != "" {
				return fmt.Errorf("%s", ev.Message)
			}
			return fmt.Errorf("pull failed")
		default:
			// ignore unknown phases for forward compatibility
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("pull stream: %w", err)
	}
	if ui.headerRef == "" {
		ui.headerRef = ref
	}
	return fmt.Errorf("pull stream: connection closed before completion")
}

// consumeRunPullStream reads NDJSON from POST /v1/containers/run with stream=true:
// zero or more pull phases (same as nyx pull), a pull "done" summary when a pull occurred,
// then a terminal {"phase":"run","container_id":...} line.
func consumeRunPullStream(r io.Reader, ref string) (string, string, error) {
	ui := newPullUI()
	defer ui.Close()

	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev image.PullEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return "", "", fmt.Errorf("run stream: decode: %w", err)
		}
		if ev.Phase == "run" {
			if ev.ContainerID == "" {
				return "", "", fmt.Errorf("run stream: missing container_id")
			}
			img := strings.TrimSpace(ev.Ref)
			if img == "" {
				img = ref
			}
			return ev.ContainerID, img, nil
		}
		switch ev.Phase {
		case "begin":
			ui.headerRef = ev.Ref
			if ev.Ref != "" {
				ui.note("\n→ Pulling %s\n\n", ev.Ref)
			}
		case "manifest":
		case "auth":
		case "meta":
			ui.note("%s\n", ev.Message)
		case "config":
			ui.ensureBar(ev.Digest, ev.Size, "config")
		case "layer":
			if ev.Cached {
				ui.note("  • %s  (already present)\n", shortDigest(ev.Digest))
				continue
			}
			ui.ensureBar(ev.Digest, ev.Size, "layer")
		case "progress":
			ui.progress(ev.Digest, ev.Current)
		case "layer_done":
			ui.closeDigest(ev.Digest)
		case "done":
			printPullSummary(os.Stdout, &ev)
		case "error":
			if ev.Message != "" {
				return "", "", fmt.Errorf("while pulling image: %s", ev.Message)
			}
			return "", "", fmt.Errorf("while pulling image: unknown error")
		default:
			// ignore unknown phases
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("run stream: %w", err)
	}
	return "", "", fmt.Errorf("run stream: connection closed before run phase")
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func printPullSummary(w io.Writer, ev *image.PullEvent) {
	r := ev.RefOut
	if r == "" {
		r = ev.Ref
	}
	fmt.Fprintf(w, "\n✔ Pulled %s\n", r)
	if ev.Config == nil {
		return
	}
	c := ev.Config
	fmt.Fprintf(w, "  OS/Arch:      %s/%s\n", c.OS, c.Architecture)
	if len(c.Entrypoint) > 0 {
		fmt.Fprintf(w, "  Entrypoint:   %s\n", strings.Join(c.Entrypoint, " "))
	}
	if len(c.Cmd) > 0 {
		fmt.Fprintf(w, "  Command:      %s\n", strings.Join(c.Cmd, " "))
	}
	if c.WorkingDir != "" {
		fmt.Fprintf(w, "  Working dir:  %s\n", c.WorkingDir)
	}
	if c.EnvLen > 0 {
		fmt.Fprintf(w, "  Environment:  %d variables\n", c.EnvLen)
	}
	if c.RootFSDiffIDs > 0 {
		fmt.Fprintf(w, "  RootFS:       %d diff layers\n", c.RootFSDiffIDs)
	}
}
