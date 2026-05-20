package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// runOpts collects `nyx run` flags and the image / command tail.
type runOpts struct {
	name     string
	image    string
	cmdArgs  []string
	detach   bool
	jsonOut  bool // print full JSON response (default: id only when -d)
	env      []string
	hostname string
	restart  string
	publish  []string
	printID  bool // foreground: print container id on stderr (default: off, docker-like)
}

func parseRunArgs(args []string) (runOpts, error) {
	var o runOpts
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-d" || a == "--detach":
			o.detach = true
			i++
		case a == "--print-id":
			o.printID = true
			i++
		case strings.HasPrefix(a, "--print-id="):
			v := strings.TrimPrefix(a, "--print-id=")
			o.printID = v == "1" || strings.EqualFold(v, "true")
			i++
		case a == "--json" || a == "-json":
			o.jsonOut = true
			i++
		case a == "-p" || a == "--publish":
			if i+1 >= len(args) {
				return o, fmt.Errorf("flag %s requires a value", a)
			}
			o.publish = append(o.publish, args[i+1])
			i += 2
		case strings.HasPrefix(a, "-p="):
			o.publish = append(o.publish, strings.TrimPrefix(a, "-p="))
			i++
		case strings.HasPrefix(a, "--publish="):
			o.publish = append(o.publish, strings.TrimPrefix(a, "--publish="))
			i++
		case a == "-e" || a == "--env":
			if i+1 >= len(args) {
				return o, fmt.Errorf("flag %s requires a value", a)
			}
			o.env = append(o.env, args[i+1])
			i += 2
		case strings.HasPrefix(a, "-e="):
			o.env = append(o.env, strings.TrimPrefix(a, "-e="))
			i++
		case strings.HasPrefix(a, "--env="):
			o.env = append(o.env, strings.TrimPrefix(a, "--env="))
			i++
		case a == "--name":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--name requires a value")
			}
			o.name = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--name="):
			o.name = strings.TrimPrefix(a, "--name=")
			i++
		case a == "--hostname":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--hostname requires a value")
			}
			o.hostname = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--hostname="):
			o.hostname = strings.TrimPrefix(a, "--hostname=")
			i++
		case a == "-h":
			if i+1 >= len(args) {
				return o, fmt.Errorf("-h requires a hostname value; use --help before the command for nyx client help")
			}
			o.hostname = args[i+1]
			i += 2
		case a == "--restart":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--restart requires a value")
			}
			o.restart = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--restart="):
			o.restart = strings.TrimPrefix(a, "--restart=")
			i++
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown flag %q", a)
			}
			goto doneFlags
		}
	}
doneFlags:
	rest := args[i:]
	if len(rest) < 1 {
		return o, fmt.Errorf("usage: nyx run [flags] <image> [-- <command args>]")
	}
	dash := -1
	for j, a := range rest {
		if a == "--" {
			dash = j
			break
		}
	}
	switch {
	case dash == 0:
		return o, fmt.Errorf("missing image before --")
	case dash > 0:
		if dash != 1 {
			return o, fmt.Errorf("expected a single image ref before --")
		}
		o.image = rest[0]
		o.cmdArgs = rest[dash+1:]
	default:
		o.image = rest[0]
	}
	return o, nil
}

// execCLIOptions is the result of parsing `nyx exec` arguments.
type execCLIOptions struct {
	ID          string
	Argv        []string
	AttachStdin bool
	WantTTY     bool // -t / -it: allocate PTY (server uses crun exec --tty + host PTY bridge)
}

func parseExecArgs(args []string) (execCLIOptions, error) {
	var o execCLIOptions
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-it" || a == "-ti":
			o.AttachStdin = true
			o.WantTTY = true
			i++
		case a == "-i" || a == "--interactive":
			o.AttachStdin = true
			i++
		case a == "-t" || a == "--tty":
			o.WantTTY = true
			i++
		case (a == "-w" || a == "--workdir") && i+1 < len(args):
			i += 2
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown exec flag %q (supported: -i, -t, -it, -w/--workdir)", a)
		default:
			goto done
		}
	}
done:
	if i >= len(args) {
		return o, fmt.Errorf("usage: nyx exec [-i] [-t] [-it] <id> [--] <command> [args...]")
	}
	o.ID = args[i]
	rest := args[i+1:]
	o.Argv = rest
	for j, a := range rest {
		if a == "--" {
			o.Argv = rest[j+1:]
			break
		}
	}
	if len(o.Argv) == 0 {
		return o, fmt.Errorf("missing command after container id")
	}
	return o, nil
}

func doPS(socket string, args []string) error {
	quiet := false
	noTrunc := false
	for _, a := range args {
		switch a {
		case "-q", "--quiet":
			quiet = true
		case "-a", "--all":
			// reserved: no stopped-container store yet
		case "--no-trunc":
			noTrunc = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown ps flag %q", a)
			}
			return fmt.Errorf("unexpected argument %q", a)
		}
	}

	c := httpClient(socket)
	u := "http://unix/v1/containers?detail=1"
	resp, err := c.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ps: %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	var out struct {
		Items []struct {
			ID      string `json:"id"`
			ShortID string `json:"short_id"`
			Image   string `json:"image"`
			IP      string `json:"ip"`
			Ports   string `json:"ports"`
			Status  string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("ps: decode: %w", err)
	}
	if quiet {
		for _, it := range out.Items {
			if it.ShortID != "" {
				fmt.Println(it.ShortID)
			} else {
				fmt.Println(it.ID)
			}
		}
		return nil
	}

	if len(out.Items) == 0 {
		fmt.Println("CONTAINER ID   IMAGE                          STATUS    PORTS                     IP")
		return nil
	}

	fmt.Println("CONTAINER ID   IMAGE                          STATUS    PORTS                     IP")
	for _, it := range out.Items {
		cid := it.ShortID
		if noTrunc {
			cid = it.ID
		} else if cid == "" {
			cid = it.ID
			if len(cid) > 12 {
				cid = cid[:12]
			}
		}
		img := it.Image
		if len(img) > 30 {
			img = img[:27] + "..."
		}
		ports := it.Ports
		if ports == "" {
			ports = "-"
		}
		if len(ports) > 24 {
			ports = ports[:21] + "..."
		}
		fmt.Printf("%-14s %-30s %-9s %-25s %s\n", cid, img, it.Status, ports, it.IP)
	}
	return nil
}

func doRM(socket string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nyx rm <id> [<id>...]")
	}
	for _, id := range args {
		if strings.HasPrefix(id, "-") {
			return fmt.Errorf("unknown flag %q", id)
		}
		if err := doRemoveOne(socket, id); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

func doRemoveOne(socket, id string) error {
	c := httpClient(socket)
	u := fmt.Sprintf("http://unix/v1/containers/%s/remove", url.PathEscape(id))
	req, err := http.NewRequest(http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	return nil
}

func doStopMany(socket string, ids []string) error {
	for _, id := range ids {
		if err := doStop(socket, id); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

func doContainer(socket string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nyx container <ls|list|rm|logs> ...")
	}
	switch args[0] {
	case "ls", "list":
		return doPS(socket, args[1:])
	case "rm":
		return doRM(socket, args[1:])
	case "logs":
		if len(args) < 2 {
			return fmt.Errorf("usage: nyx container logs <id> [flags...]")
		}
		return doLogs(socket, args[1:])
	default:
		return fmt.Errorf("unknown container subcommand %q (try ls, list, rm, logs)", args[0])
	}
}

func streamContainerLogs(ctx context.Context, socket, id string, dest io.Writer) error {
	q := url.Values{}
	q.Set("follow", "1")
	q.Set("plain", "1")
	q.Set("tail", "99999")
	u := fmt.Sprintf("http://unix/v1/containers/%s/logs?%s", url.PathEscape(id), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	_, err = io.Copy(dest, resp.Body)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func parseLogsArgs(args []string) (id string, follow bool, tail int, err error) {
	tail = 200
	var ids []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--follow":
			follow = true
		case (a == "--tail" || a == "-n") && i+1 < len(args):
			tail, err = strconv.Atoi(args[i+1])
			if err != nil {
				return "", false, 0, fmt.Errorf("invalid tail: %w", err)
			}
			i++
		case strings.HasPrefix(a, "--tail="):
			tail, err = strconv.Atoi(strings.TrimPrefix(a, "--tail="))
			if err != nil {
				return "", false, 0, fmt.Errorf("invalid --tail=: %w", err)
			}
		default:
			if strings.HasPrefix(a, "-") {
				return "", false, 0, fmt.Errorf("unknown flag %q", a)
			}
			ids = append(ids, a)
		}
	}
	if len(ids) != 1 {
		return "", false, 0, fmt.Errorf("usage: nyx logs [-f] [--tail N|-n N] <id>")
	}
	return ids[0], follow, tail, nil
}

func doLogs(socket string, args []string) error {
	id, follow, tail, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	q := url.Values{}
	if follow {
		q.Set("follow", "1")
	}
	q.Set("plain", "1")
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	u := fmt.Sprintf("http://unix/v1/containers/%s/logs?%s", url.PathEscape(id), q.Encode())

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("logs: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func doImage(socket string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nyx image <ls|list|rm|prune> ...")
	}
	switch args[0] {
	case "ls", "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: nyx image ls")
		}
		return doImageList(socket)
	case "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: nyx image rm <ref> [<ref>...]")
		}
		for _, ref := range args[1:] {
			if strings.HasPrefix(ref, "-") {
				return fmt.Errorf("unknown flag %q", ref)
			}
			if err := doImageRmOne(socket, ref); err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
		}
		return nil
	case "prune":
		return doImagePrune(socket, args[1:])
	default:
		return fmt.Errorf("unknown image subcommand %q (try ls, rm, prune)", args[0])
	}
}

func doImageList(socket string) error {
	resp, err := httpClient(socket).Get("http://unix/v1/images")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image ls: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var out struct {
		Images []string `json:"images"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	for _, ref := range out.Images {
		fmt.Println(ref)
	}
	return nil
}

func doImageRmOne(socket, ref string) error {
	raw, _ := json.Marshal(map[string]string{"ref": ref})
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/images/remove", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	return nil
}

func doImagePrune(socket string, args []string) error {
	dryRun := false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag %q", a)
			}
			return fmt.Errorf("unexpected argument %q (only --dry-run / -n allowed)", a)
		}
	}
	raw, _ := json.Marshal(map[string]bool{"dry_run": dryRun})
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/images/prune", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image prune: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	var out struct {
		OK      bool     `json:"ok"`
		DryRun  bool     `json:"dry_run"`
		Removed []string `json:"removed"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	if len(out.Removed) == 0 {
		fmt.Println("nothing to prune")
		return nil
	}
	action := "Removed"
	if out.DryRun {
		action = "Would remove"
	}
	fmt.Printf("%s %d image(s):\n", action, len(out.Removed))
	for _, ref := range out.Removed {
		fmt.Println(ref)
	}
	return nil
}
