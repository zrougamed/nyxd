package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
)

const updateUsage = `usage: nyx update [--check] [--json]

Update the containers labelled nyx.update=digest whose image tag points to
a new digest: pull, recreate, and put the previous image back if the new
one does not start or does not pass its health check.
  --check   only report what would change (no pull, no restart)
  --json    print the daemon's answer as JSON
nyxd -update-interval (e.g. 6h) does the same on a schedule.`

func doUpdate(socket string, args []string) error {
	check, jsonOut := false, false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println(updateUsage)
			return nil
		default:
			return fmt.Errorf("unknown flag %q\n%s", a, updateUsage)
		}
	}
	method := http.MethodPost
	if check {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, "http://unix/v1/updates", nil)
	if err != nil {
		return err
	}
	resp, err := streamClient(socket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("update: this nyxd is too old (upgrade the daemon)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: %w", daemonError(resp.StatusCode, b))
	}
	if jsonOut {
		_, err := os.Stdout.Write(b)
		return err
	}
	var out struct {
		Containers []struct {
			ID, Image, Current, Available, Action, Error string
		} `json:"containers"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("update: bad response: %w", err)
	}
	if len(out.Containers) == 0 {
		fmt.Println("no running container is labelled nyx.update=digest")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER\tIMAGE\tRESULT")
	failed := false
	for _, c := range out.Containers {
		res := c.Action
		if c.Available != "" && c.Action != "error" {
			res += " (" + shortDigest(c.Available) + ")"
		}
		if c.Error != "" {
			res += ": " + c.Error
			failed = failed || c.Action == "error"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.ID, c.Image, res)
	}
	_ = tw.Flush()
	if failed {
		return fmt.Errorf("update: some containers could not be checked or updated")
	}
	return nil
}
