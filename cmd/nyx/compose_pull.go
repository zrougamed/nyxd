package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/image"
)

// doComposePull is nyx compose pull [SERVICE...]: pull every service's image
// in parallel (services with build: only with --include-build), one line
// per service. compose up then recreates the services whose image changed.
func doComposePull(socket string, opts composeCLI) error {
	abs, err := resolveComposePathForCLI(opts.file)
	if err != nil {
		return err
	}
	st, err := compose.ParseFile(abs)
	if err != nil {
		return err
	}
	names := opts.positional
	if len(names) == 0 {
		for n := range st.Services {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	type job struct{ service, ref string }
	var jobs []job
	for _, n := range names {
		svc, ok := st.Services[n]
		if !ok {
			return fmt.Errorf("unknown service %q", n)
		}
		ref := strings.TrimSpace(svc.Image)
		if svc.Build != nil && !opts.includeBuild {
			fmt.Fprintf(os.Stderr, "%s: skipped (build:, use --include-build to pull its image)\n", n)
			continue
		}
		if ref == "" {
			fmt.Fprintf(os.Stderr, "%s: skipped (no image)\n", n)
			continue
		}
		jobs = append(jobs, job{n, ref})
	}

	var (
		mu     sync.Mutex
		failed []string
		wg     sync.WaitGroup
		sem    = make(chan struct{}, 4)
	)
	for _, j := range jobs {
		svc := st.Services[j.service]
		auth := &image.RegistryAuth{Username: svc.RegistryUsername, Password: svc.RegistryPassword}
		if strings.TrimSpace(auth.Username) == "" {
			auth.Username, auth.Password = registryCredentials(j.ref)
		}
		wg.Add(1)
		go func(j job, auth *image.RegistryAuth) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			newLayers, err := pullQuiet(socket, j.ref, auth)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed = append(failed, j.service)
				fmt.Fprintf(os.Stderr, "%s (%s): %v\n", j.service, j.ref, err)
			case newLayers == 0:
				fmt.Fprintf(os.Stderr, "%s (%s): up to date\n", j.service, j.ref)
			default:
				fmt.Fprintf(os.Stderr, "%s (%s): pulled, %d new layer(s)\n", j.service, j.ref, newLayers)
			}
		}(j, auth)
	}
	wg.Wait()
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("compose pull failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// pullQuiet pulls ref through the streaming pull API without drawing
// progress, and returns how many layers had to be downloaded.
func pullQuiet(socket, ref string, auth *image.RegistryAuth) (int, error) {
	req := map[string]any{"ref": ref, "stream": true}
	if auth != nil && strings.TrimSpace(auth.Username) != "" {
		req["username"], req["password"] = auth.Username, auth.Password
	}
	body, _ := json.Marshal(req)
	resp, err := httpClient(socket).Post("http://unix/v1/images/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, daemonError(resp.StatusCode, b)
	}
	newLayers, done := 0, false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev image.PullEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Phase {
		case "layer":
			if !ev.Cached {
				newLayers++
			}
		case "error":
			return newLayers, fmt.Errorf("%s", strings.TrimSpace(ev.Message))
		case "done":
			done = true
		}
	}
	if err := sc.Err(); err != nil {
		return newLayers, err
	}
	if !done {
		return newLayers, fmt.Errorf("pull ended early")
	}
	return newLayers, nil
}

// printComposeResults prints what compose up did with each service.
func printComposeResults(w io.Writer, svcs []any) {
	for _, v := range svcs {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["service"].(string)
		if name == "" {
			name, _ = m["id"].(string)
		}
		line := name + ": " + fmt.Sprint(m["action"])
		if reason, _ := m["reason"].(string); reason != "" {
			line += " (" + reason + ")"
		}
		fmt.Fprintln(w, line)
	}
}
