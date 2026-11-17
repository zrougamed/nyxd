package control

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":                     {},
		"10m":                  now.Add(-10 * time.Minute),
		"2026-11-17T11:00:00Z": now.Add(-time.Hour),
		"1763380800":           time.Unix(1763380800, 0),
	} {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"yesterday", "-5m", "12abc"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEventsEndpoint(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "v1", "c", "d", t.TempDir(), "", "", "host", "auto")
	rec := httptest.NewRecorder()
	s.handleEvents(rec, httptest.NewRequest("GET", "/v1/events", nil))
	if rec.Code != 503 {
		t.Fatalf("no bus: %d", rec.Code)
	}
	bus := events.New(10)
	s.SetEvents(bus)
	bus.Publish(events.Event{Type: events.TypeContainer, Action: events.Start, ID: "old"})
	srv := httptest.NewServer(http.HandlerFunc(s.handleEvents))
	defer srv.Close()

	for _, bad := range []string{"?since=whenever", "?filter=color=red"} {
		resp, err := http.Get(srv.URL + bad)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d", bad, resp.StatusCode)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"?since=1h&filter=container=web&filter=container=old", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan events.Event, 10)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var e events.Event
			_ = json.Unmarshal(sc.Bytes(), &e)
			lines <- e
		}
		close(lines)
	}()
	next := func() events.Event {
		select {
		case e := <-lines:
			return e
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
			return events.Event{}
		}
	}
	if e := next(); e.ID != "old" {
		t.Fatalf("replay: %+v", e)
	}
	// Give the handler time to subscribe-and-flush, then publish live ones.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.Event{Type: events.TypeContainer, Action: events.Die, ID: "other"})
	bus.Publish(events.Event{Type: events.TypeContainer, Action: events.Die, ID: "web", Attrs: map[string]string{"exit_code": "3"}})
	if e := next(); e.ID != "web" || e.Attrs["exit_code"] != "3" {
		t.Fatalf("live filtered: %+v", e)
	}
	cancel()
	for range lines {
	}
}
