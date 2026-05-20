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

	"github.com/hedzr/progressbar"
	"github.com/zrougamed/nyxd/internal/image"
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

type pullBar struct {
	ch   chan int64
	once sync.Once
}

func (b *pullBar) send(delta int64) {
	if delta > 0 {
		b.ch <- delta
	}
}

func (b *pullBar) close() {
	b.once.Do(func() { close(b.ch) })
}

type pullUI struct {
	mpb       progressbar.MultiPB
	bars      map[string]*pullBar
	lastAbs   map[string]int64
	mu        sync.Mutex
	headerRef string
}

func newPullUI() *pullUI {
	mpb := progressbar.New(progressbar.WithOutputDevice(os.Stderr))
	return &pullUI{
		mpb:     mpb,
		bars:    make(map[string]*pullBar),
		lastAbs: make(map[string]int64),
	}
}

func (u *pullUI) ensureBar(digest string, size int64, subtitle string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.bars[digest]; ok {
		return
	}
	pb := &pullBar{ch: make(chan int64, 256)}
	u.bars[digest] = pb

	title := shortDigest(digest)
	if subtitle != "" {
		title = title + "  " + subtitle
	}
	max := size
	if max <= 0 {
		max = 1 << 30
	}

	u.mpb.Add(max, title,
		progressbar.WithBarStepper(0),
		progressbar.WithBarWorker(func(bar progressbar.PB, exit <-chan struct{}) bool {
			for {
				select {
				case d, ok := <-pb.ch:
					if !ok {
						_, ub, p := bar.Bounds()
						if ub > 0 && p < ub {
							bar.Step(ub - p)
						}
						return false
					}
					if d > 0 {
						bar.Step(d)
					}
				case <-exit:
					return false
				}
			}
		}),
	)
}

func (u *pullUI) progress(digest string, current int64) {
	u.mu.Lock()
	pb, ok := u.bars[digest]
	last := u.lastAbs[digest]
	u.mu.Unlock()
	if !ok || pb == nil {
		return
	}
	delta := current - last
	if delta < 0 {
		delta = 0
	}
	u.mu.Lock()
	u.lastAbs[digest] = current
	u.mu.Unlock()
	if delta > 0 {
		pb.send(delta)
	}
}

func (u *pullUI) closeDigest(digest string) {
	u.mu.Lock()
	pb, ok := u.bars[digest]
	u.mu.Unlock()
	if ok && pb != nil {
		pb.close()
	}
}

func (u *pullUI) Close() {
	u.mu.Lock()
	for _, pb := range u.bars {
		pb.close()
	}
	u.mu.Unlock()
	u.mpb.Close()
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
				fmt.Fprintf(os.Stderr, "\n→ Pulling %s\n\n", ev.Ref)
			}
		case "manifest":
			// optional context
		case "auth":
		case "meta":
			fmt.Fprintf(os.Stderr, "%s\n", ev.Message)
		case "config":
			ui.ensureBar(ev.Digest, ev.Size, "config")
		case "layer":
			if ev.Cached {
				fmt.Fprintf(os.Stderr, "  • %s  (already present)\n", shortDigest(ev.Digest))
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
				fmt.Fprintf(os.Stderr, "\n→ Pulling %s\n\n", ev.Ref)
			}
		case "manifest":
		case "auth":
		case "meta":
			fmt.Fprintf(os.Stderr, "%s\n", ev.Message)
		case "config":
			ui.ensureBar(ev.Digest, ev.Size, "config")
		case "layer":
			if ev.Cached {
				fmt.Fprintf(os.Stderr, "  • %s  (already present)\n", shortDigest(ev.Digest))
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
