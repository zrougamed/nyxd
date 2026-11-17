package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/events"
)

// SetEvents sets the event bus: GET /v1/events streams it, and image
// pulls, removals and builds publish to it.
func (s *Server) SetEvents(b *events.Bus) { s.events = b }

func (s *Server) imageEvent(action, ref string) {
	s.events.Publish(events.Event{Type: events.TypeImage, Action: action, ID: ref})
}

// parseSince reads since= as a duration back from now ("10m") or a
// timestamp (RFC 3339 or Unix seconds).
func parseSince(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	var secs int64
	if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && fmt.Sprint(secs) == v {
		return time.Unix(secs, 0), nil
	}
	return time.Time{}, fmt.Errorf("invalid since %q: want a duration (10m) or a time (RFC 3339)", v)
}

// handleEvents is GET /v1/events: newline-delimited JSON events, kept open
// until the client goes away. since= replays kept events first; filter=k=v
// (repeatable) selects (type, event, container, image). A client that
// cannot keep up gets a last {"type":"events_dropped"} line.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.events == nil {
		http.Error(w, "events not available", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	since, err := parseSince(q.Get("since"), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter, err := events.ParseFilter(q["filter"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	past, sub := s.events.Subscribe(since, filter)
	defer sub.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	write := func(v any) bool {
		if err := enc.Encode(v); err != nil {
			return false
		}
		if fl != nil {
			fl.Flush()
		}
		return true
	}
	for _, e := range past {
		if !write(e) {
			return
		}
	}
	if fl != nil {
		fl.Flush() // headers out even with no history
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				if sub.Dropped() {
					write(map[string]any{"time": time.Now().UTC(), "type": "events_dropped",
						"message": "this client fell behind; reconnect with since= to catch up"})
				}
				return
			}
			if !write(e) {
				return
			}
		}
	}
}
