package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEventsCommand(t *testing.T) {
	stream := `{"time":"2026-11-17T22:55:10Z","type":"image","action":"pull","id":"alpine:3.20"}
{"time":"2026-11-17T22:55:23Z","type":"container","action":"die","id":"web","image":"alpine:3.20","attributes":{"signal":"9","exit_code":"137"}}
not json
`
	var query string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(stream))
	})
	out, _, err := capture(t, "", func() error {
		return run([]string{"-socket", sock, "events", "--filter", "container=web", "--filter=event=die", "--since", "10m"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "filter=container%3Dweb") || !strings.Contains(query, "filter=event%3Ddie") || !strings.Contains(query, "since=10m") {
		t.Errorf("query %q", query)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " image pull alpine:3.20") ||
		!strings.HasSuffix(lines[1], " container die web (image=alpine:3.20, exit_code=137, signal=9)") {
		t.Fatalf("output:\n%s", out)
	}
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", sock, "events", "--json"}) })
	if !strings.Contains(out, `"action":"pull"`) || strings.Contains(out, "not json") {
		t.Errorf("--json:\n%s", out)
	}
	dropped := stub(t, reply(200, `{"time":"2026-11-17T22:00:00Z","type":"events_dropped","message":"fell behind"}`+"\n"+`{"x":1}`+"\n"))
	if err := run([]string{"-socket", dropped, "events"}); err == nil || !strings.Contains(err.Error(), "fell behind") {
		t.Errorf("dropped: %v", err)
	}
	for _, args := range [][]string{{"events", "--filter"}, {"events", "--bogus"}, {"events", "--since"}} {
		if err := run(append([]string{"-socket", sock}, args...)); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if err := run([]string{"-socket", sock, "events", "--help"}); err != nil {
		t.Error(err)
	}
	old := stub(t, reply(404, "404 page not found"))
	if err := run([]string{"-socket", old, "events"}); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("old: %v", err)
	}
	broken := stub(t, reply(400, "invalid filter"))
	if err := run([]string{"-socket", broken, "events"}); err == nil {
		t.Error("400 accepted")
	}
	if got := formatEvent(eventJSON{Type: "events_dropped", Time: time.Unix(0, 0).UTC(), Msg: "x"}); !strings.Contains(got, "events_dropped x") {
		t.Error(got)
	}
}
