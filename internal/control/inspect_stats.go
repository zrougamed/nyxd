package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleContainerInspect answers GET /v1/containers/{id}/inspect.
func (s *Server) handleContainerInspect(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		http.Error(w, "supervisor unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	canon, err := s.resolveContainerID(id)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	info, err := s.sup.Inspect(r.Context(), canon)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// handleContainerStats answers GET /v1/containers/stats.
// Optional query: ids=a,b (canonical or short; empty = all), interval_ms (default 500).
func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		http.Error(w, "supervisor unavailable", http.StatusServiceUnavailable)
		return
	}
	noWriteDeadline(w) // sampling can take a second or more

	var ids []string
	if raw := strings.TrimSpace(r.URL.Query().Get("ids")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			canon, err := s.resolveContainerID(part)
			if err != nil {
				writeResolveError(w, err)
				return
			}
			ids = append(ids, canon)
		}
	}
	interval := 500 * time.Millisecond
	if v := strings.TrimSpace(r.URL.Query().Get("interval_ms")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Millisecond
			if interval > 10*time.Second {
				interval = 10 * time.Second
			}
		}
	}
	items, err := s.sup.Stats(r.Context(), ids, interval)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}
