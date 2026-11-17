package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/internal/layer"
)

const cpUsage = `usage: nyx cp [-a] CONTAINER:SRC DEST|-
       nyx cp [-a] SRC|- CONTAINER:DEST

Copy files or directories between the host and a running container.
  -               write the copied files as a tar stream to stdout, or read
                  one from stdin (extracted into the existing directory DEST)
  -a, --archive   keep the files' uid and gid when copying into a container
                  (default: owned by root, like docker cp)

When DEST is an existing directory the copy lands inside it; otherwise it
is created (or replaced) under that name.`

// cpSpec splits "container:/path". Local paths start with / or . (or are
// "-"), so "./a:b" stays local.
func cpSpec(arg string) (container, p string, remote bool) {
	if arg == "-" || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".") {
		return "", arg, false
	}
	if c, rest, ok := strings.Cut(arg, ":"); ok && c != "" {
		return c, rest, true
	}
	return "", arg, false
}

func doCp(socket string, args []string) error {
	archive := false
	var pos []string
	for _, a := range args {
		switch {
		case a == "-a" || a == "--archive":
			archive = true
		case a == "-h" || a == "--help":
			fmt.Println(cpUsage)
			return nil
		case strings.HasPrefix(a, "-") && a != "-":
			return fmt.Errorf("unknown flag %q\n%s", a, cpUsage)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		return fmt.Errorf("%s", cpUsage)
	}
	srcC, src, srcRemote := cpSpec(pos[0])
	dstC, dst, dstRemote := cpSpec(pos[1])
	switch {
	case srcRemote && dstRemote:
		return fmt.Errorf("copying between two containers is not supported: copy to the host first")
	case srcRemote:
		return cpFromContainer(socket, srcC, src, dst)
	case dstRemote:
		return cpToContainer(socket, src, dstC, dst, archive)
	default:
		return fmt.Errorf("one of SRC and DEST must be CONTAINER:PATH\n%s", cpUsage)
	}
}

func archiveURL(container, p string, extra url.Values) string {
	q := url.Values{"path": {p}}
	for k, v := range extra {
		q[k] = v
	}
	return "http://unix/v1/containers/" + url.PathEscape(container) + "/archive?" + q.Encode()
}

func cpFromContainer(socket, container, src, dst string) error {
	resp, err := httpClient(socket).Get(archiveURL(container, src, nil))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("cp: %w", daemonError(resp.StatusCode, b))
	}
	if dst == "-" {
		_, err := io.Copy(os.Stdout, resp.Body)
		return err
	}
	cr := &countReader{r: resp.Body}
	var body io.Reader = cr
	dir := dst
	if fi, err := os.Stat(dst); err != nil || !fi.IsDir() {
		dir = filepath.Dir(dst)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("cp: %s: no such directory", dir)
		}
		body = layer.RenameTop(cr, filepath.Base(dst))
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	// Literal: names like .wh.x are files. No chown: the copy belongs to
	// whoever runs nyx. Writes stay inside dir whatever the archive says.
	if _, err := layer.ApplyRoot(body, root, layer.ApplyOptions{Mode: layer.ModeFlatten, Literal: true}); err != nil {
		return fmt.Errorf("cp: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Copied %s to %s\n", humanBytes(cr.n), dst)
	return nil
}

func cpToContainer(socket, src, container, dst string, archive bool) error {
	var body io.Reader
	extra := url.Values{}
	if src == "-" {
		body = os.Stdin
		extra.Set("raw", "1")
	} else {
		abs, err := filepath.Abs(src)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(abs); err != nil {
			return fmt.Errorf("cp: %w", err)
		}
		root, err := os.OpenRoot(filepath.Dir(abs))
		if err != nil {
			return err
		}
		defer root.Close()
		var owner *layer.Owner
		if !archive {
			owner = &layer.Owner{}
		}
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(layer.WriteTree(pw, root, filepath.Base(abs), filepath.Base(abs), owner))
		}()
		body = pr
	}
	cr := &countReader{r: body}
	req, err := http.NewRequest(http.MethodPut, archiveURL(container, dst, extra), cr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := httpClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cp: %w", daemonError(resp.StatusCode, b))
	}
	fmt.Fprintf(os.Stderr, "Copied %s to %s:%s\n", humanBytes(cr.n), container, dst)
	return nil
}

type countReader struct {
	r io.Reader
	n uint64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += uint64(n)
	return n, err
}
