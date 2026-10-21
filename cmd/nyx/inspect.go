package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func doInspect(socket string, args []string) error {
	var ids []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown inspect flag %q (JSON output only; no --format yet)", a)
		}
		ids = append(ids, a)
	}
	if len(ids) == 0 {
		return fmt.Errorf("usage: nyx inspect <id> [<id>...]")
	}
	var out []json.RawMessage
	for _, id := range ids {
		raw, err := fetchInspect(socket, id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, raw)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func fetchInspect(socket, id string) (json.RawMessage, error) {
	u := fmt.Sprintf("http://unix/v1/containers/%s/inspect", url.PathEscape(id))
	resp, err := httpClient(socket).Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, daemonError(resp.StatusCode, body)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("bad response: %s", strings.TrimSpace(string(body)))
	}
	return json.RawMessage(body), nil
}
