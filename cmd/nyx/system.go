package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

const systemUsage = `usage: nyx system <df|prune> [flags]

  df [-v] [--json]      disk use: images, build cache, containers, logs, volumes
                        (-v: per item)
  prune [--images] [--volumes] [--dry-run]
                        remove what nothing references: exited containers and
                        their logs, logs and overlays left by containers that no
                        longer exist, the build cache and layers no image uses.
                        --images also removes images no container uses,
                        --volumes also volumes no container mounts.
                        Running containers and what they use are never touched.`

func doSystem(socket string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", systemUsage)
	}
	switch args[0] {
	case "df":
		return doSystemDF(socket, args[1:])
	case "prune":
		return doSystemPrune(socket, args[1:])
	case "-h", "--help":
		fmt.Println(systemUsage)
		return nil
	default:
		return fmt.Errorf("unknown system subcommand %q\n%s", args[0], systemUsage)
	}
}

type dfCategoryJSON struct {
	Type        string `json:"type"`
	Count       int    `json:"count"`
	Active      int    `json:"active"`
	Size        uint64 `json:"size"`
	Reclaimable uint64 `json:"reclaimable"`
}

type dfItemJSON struct {
	Name   string `json:"name"`
	Size   uint64 `json:"size"`
	Unique uint64 `json:"unique"`
	Active bool   `json:"active"`
	Detail string `json:"detail"`
}

type dfJSON struct {
	Categories []dfCategoryJSON        `json:"categories"`
	Items      map[string][]dfItemJSON `json:"items"`
}

var dfTitles = map[string]string{
	"images": "Images", "build_cache": "Build cache", "containers": "Containers", "logs": "Logs", "volumes": "Volumes",
}

func doSystemDF(socket string, args []string) error {
	verbose, jsonOut := false, false
	for _, a := range args {
		switch a {
		case "-v", "--verbose":
			verbose = true
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("unknown flag %q\n%s", a, systemUsage)
		}
	}
	resp, err := httpClient(socket).Get("http://unix/v1/system/df")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("system df: this nyxd is too old (upgrade the daemon)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("system df: %w", daemonError(resp.StatusCode, b))
	}
	if jsonOut {
		_, err := os.Stdout.Write(b)
		return err
	}
	var df dfJSON
	if err := json.Unmarshal(b, &df); err != nil {
		return fmt.Errorf("system df: bad response: %w", err)
	}
	writeDF(os.Stdout, df, verbose)
	return nil
}

func writeDF(w io.Writer, df dfJSON, verbose bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tCOUNT\tACTIVE\tSIZE\tRECLAIMABLE")
	for _, c := range df.Categories {
		active := fmt.Sprint(c.Active)
		if c.Type == "build_cache" {
			active = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", or(dfTitles[c.Type], c.Type), c.Count, active, humanBytes(c.Size), humanBytes(c.Reclaimable))
	}
	_ = tw.Flush()
	if !verbose {
		return
	}
	for _, kind := range []string{"images", "containers", "logs", "volumes"} {
		items := df.Items[kind]
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		fmt.Fprintf(w, "\n%s:\n", dfTitles[kind])
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		switch kind {
		case "images":
			fmt.Fprintln(tw, "REFERENCE\tSIZE\tUNIQUE\tIN USE")
			for _, it := range items {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.Name, humanBytes(it.Size), humanBytes(it.Unique), yesNo(it.Active))
			}
		case "containers":
			fmt.Fprintln(tw, "CONTAINER\tSIZE\tSTATE")
			for _, it := range items {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Name, humanBytes(it.Size), it.Detail)
			}
		case "logs":
			fmt.Fprintln(tw, "CONTAINER\tSIZE\tRUNNING")
			for _, it := range items {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Name, humanBytes(it.Size), yesNo(it.Active))
			}
		case "volumes":
			fmt.Fprintln(tw, "VOLUME\tSIZE\tIN USE\tPATH")
			for _, it := range items {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.Name, humanBytes(it.Size), yesNo(it.Active), it.Detail)
			}
		}
		_ = tw.Flush()
	}
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func doSystemPrune(socket string, args []string) error {
	req := map[string]bool{}
	for _, a := range args {
		switch a {
		case "--images", "-a", "--all":
			req["images"] = true
		case "--volumes":
			req["volumes"] = true
		case "--dry-run", "-n":
			req["dry_run"] = true
		default:
			return fmt.Errorf("unknown flag %q\n%s", a, systemUsage)
		}
	}
	body, _ := json.Marshal(req)
	resp, err := httpClient(socket).Post("http://unix/v1/system/prune", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("system prune: this nyxd is too old (upgrade the daemon)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("system prune: %w", daemonError(resp.StatusCode, b))
	}
	var out struct {
		DryRun    bool                `json:"dry_run"`
		Removed   map[string][]string `json:"removed"`
		Reclaimed uint64              `json:"reclaimed"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("system prune: bad response: %w", err)
	}
	verb := "Removed"
	if out.DryRun {
		verb = "Would remove"
	}
	for _, kind := range []string{"containers", "images", "volumes", "logs", "overlays", "build_cache", "layers"} {
		names := out.Removed[kind]
		if len(names) == 0 {
			continue
		}
		label := strings.ReplaceAll(kind, "_", " ")
		fmt.Printf("%s %d %s\n", verb, len(names), label)
		if kind != "build_cache" && kind != "layers" {
			for _, n := range names {
				fmt.Printf("  %s\n", n)
			}
		}
	}
	if out.DryRun {
		fmt.Printf("Would reclaim about %s\n", humanBytes(out.Reclaimed))
	} else {
		fmt.Printf("Reclaimed %s\n", humanBytes(out.Reclaimed))
	}
	return nil
}
