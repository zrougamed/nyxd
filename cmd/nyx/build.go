package main

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/internal/build/dockerignore"
)

type buildOpts struct {
	tags       []string
	file       string
	target     string
	buildArgs  []string
	noCache    bool
	quiet      bool
	contextDir string
}

// injectedDockerfile is where a Dockerfile from outside the context is placed.
const injectedDockerfile = ".nyx-build/Dockerfile"

func parseBuildArgs(args []string) (buildOpts, error) {
	var o buildOpts
	value := func(i *int, name string) (string, error) {
		a := args[*i]
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("flag %s requires a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, _ := strings.Cut(a, "=")
		var err error
		var v string
		switch name {
		case "-t", "--tag":
			v, err = value(&i, name)
			o.tags = append(o.tags, v)
		case "-f", "--file":
			o.file, err = value(&i, name)
		case "--target":
			o.target, err = value(&i, name)
		case "--build-arg":
			v, err = value(&i, name)
			if err == nil && !strings.Contains(v, "=") {
				// Docker: "--build-arg NAME" takes the value from the environment.
				if ev, ok := os.LookupEnv(v); ok {
					v = v + "=" + ev
				} else {
					continue
				}
			}
			o.buildArgs = append(o.buildArgs, v)
		case "--no-cache":
			o.noCache = true
		case "-q", "--quiet":
			o.quiet = true
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown flag %q", a)
			}
			if o.contextDir != "" {
				return o, fmt.Errorf("only one build context allowed (got %q and %q)", o.contextDir, a)
			}
			o.contextDir = a
		}
		if err != nil {
			return o, err
		}
	}
	if o.contextDir == "" {
		return o, errors.New("usage: nyx build [-t TAG]... [-f Dockerfile] [--build-arg K=V]... [--target STAGE] [--no-cache] [-q] <context-dir>")
	}
	return o, nil
}

func doBuild(socket string, args []string) error {
	o, err := parseBuildArgs(args)
	if err != nil {
		return err
	}
	ctxDir, err := filepath.Abs(o.contextDir)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(ctxDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("build context %s is not a directory", o.contextDir)
	}
	dfPath := o.file
	if dfPath == "" {
		dfPath = filepath.Join(ctxDir, "Dockerfile")
	} else if !filepath.IsAbs(dfPath) {
		if dfPath, err = filepath.Abs(dfPath); err != nil {
			return err
		}
	}
	if _, err := os.Stat(dfPath); err != nil {
		return fmt.Errorf("Dockerfile: %w", err)
	}
	dfParam := injectedDockerfile
	if rel, err := filepath.Rel(ctxDir, dfPath); err == nil && !strings.HasPrefix(rel, "..") {
		dfParam = filepath.ToSlash(rel)
	}

	q := url.Values{}
	for _, t := range o.tags {
		q.Add("t", t)
	}
	for _, b := range o.buildArgs {
		q.Add("buildarg", b)
	}
	q.Set("dockerfile", dfParam)
	if o.target != "" {
		q.Set("target", o.target)
	}
	if o.noCache {
		q.Set("nocache", "1")
	}

	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeContext(pw, ctxDir, dfPath, dfParam))
	}()

	// No overall timeout: builds can take long; the daemon stops on disconnect.
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/build?"+q.Encode(), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("build: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Stream  string   `json:"stream"`
			Error   string   `json:"error"`
			Done    bool     `json:"done"`
			ImageID string   `json:"image_id"`
			Tags    []string `json:"tags"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch {
		case ev.Error != "":
			return fmt.Errorf("build failed: %s", ev.Error)
		case ev.Done:
			if o.quiet {
				fmt.Println(ev.ImageID)
			} else {
				fmt.Printf("Successfully built %s\n", strings.TrimPrefix(ev.ImageID, "sha256:")[:12])
				for _, t := range ev.Tags {
					fmt.Printf("Successfully tagged %s\n", t)
				}
			}
			return nil
		case !o.quiet:
			fmt.Print(ev.Stream)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("build: connection closed before the build finished")
}

// writeContext streams ctxDir as a tar, honouring .dockerignore. The
// Dockerfile and .dockerignore are always sent; a Dockerfile outside the
// context is added as injectedDockerfile.
func writeContext(w io.Writer, ctxDir, dfPath, dfParam string) error {
	var m *dockerignore.Matcher
	if f, err := os.Open(filepath.Join(ctxDir, ".dockerignore")); err == nil {
		m, err = dockerignore.Parse(f)
		f.Close()
		if err != nil {
			return fmt.Errorf(".dockerignore: %w", err)
		}
	}
	keep := map[string]bool{".dockerignore": true, dfParam: true}

	tw := tar.NewWriter(w)
	err := filepath.WalkDir(ctxDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == ctxDir {
			return nil
		}
		rel, err := filepath.Rel(ctxDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !keep[rel] && m.Excluded(rel) {
			if d.IsDir() && m.CanSkipDir() && !keepsBelow(keep, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		return addToTar(tw, p, rel)
	})
	if err != nil {
		return err
	}
	if dfParam == injectedDockerfile {
		if err := addToTar(tw, dfPath, injectedDockerfile); err != nil {
			return err
		}
	}
	return tw.Close()
}

func keepsBelow(keep map[string]bool, dir string) bool {
	for k := range keep {
		if strings.HasPrefix(k, dir+"/") {
			return true
		}
	}
	return false
}

func addToTar(tw *tar.Writer, p, name string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	link := ""
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		if link, err = os.Readlink(p); err != nil {
			return err
		}
	case fi.IsDir(), fi.Mode().IsRegular():
	default:
		return nil // sockets, devices, fifos are not part of a build context
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	hdr.Name = name
	if fi.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
	hdr.Format = tar.FormatPAX
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.CopyN(tw, f, hdr.Size)
	return err
}
