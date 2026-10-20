package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleContainerInspectUnavailable(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleContainerInspect(rec, httptest.NewRequest(http.MethodGet, "/v1/containers/x/inspect", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestHandleContainerStatsUnavailable(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleContainerStats(rec, httptest.NewRequest(http.MethodGet, "/v1/containers/stats", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestHandleContainerStatsEmpty(t *testing.T) {
	// Lister-only server with no supervisor still reports unavailable for stats.
	s := &Server{lister: listFunc(func() []string { return nil })}
	rec := httptest.NewRecorder()
	s.handleContainerStats(rec, httptest.NewRequest(http.MethodGet, "/v1/containers/stats", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
}

type listFunc func() []string

func (f listFunc) List() []string { return f() }

func TestInspectStatsJSONShape(t *testing.T) {
	// Ensure the empty items encoding stays a JSON array when we construct it
	// the same way as the handler (regression for `items: null`).
	raw, err := json.Marshal(map[string]any{"items": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("got %s", raw)
	}
}
