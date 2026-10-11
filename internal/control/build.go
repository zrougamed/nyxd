package control

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/zrougamed/nyxd/internal/build"
	"github.com/zrougamed/nyxd/internal/layer"
)

// SetBuilder enables POST /v1/build.
func (s *Server) SetBuilder(b *build.Builder) { s.builder = b }

// buildEvent is one NDJSON line of a build response.
type buildEvent struct {
	Stream  string   `json:"stream,omitempty"`
	Error   string   `json:"error,omitempty"`
	Done    bool     `json:"done,omitempty"`
	ImageID string   `json:"image_id,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

type ndjsonWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	fl  http.Flusher
}

func (w *ndjsonWriter) send(ev buildEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(ev)
	if w.fl != nil {
		w.fl.Flush()
	}
}

func (w *ndjsonWriter) Write(p []byte) (int, error) {
	w.send(buildEvent{Stream: string(p)})
	return len(p), nil
}

// handleBuild builds an image from a Dockerfile.
//
// Body: build context as a tar (optionally gzip-compressed).
// Query: t (repeatable tag), dockerfile (path in the context, default
// "Dockerfile"), target, nocache=1, buildarg=KEY=VALUE (repeatable).
// Response: application/x-ndjson with {"stream":...} progress lines, then
// {"done":true,"image_id":...,"tags":[...]} or {"error":...}.
func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	if s.builder == nil {
		http.Error(w, "builder not configured", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	opts := build.Options{
		Dockerfile: q.Get("dockerfile"),
		Tags:       q["t"],
		Target:     q.Get("target"),
		NoCache:    q.Get("nocache") == "1" || q.Get("nocache") == "true",
		BuildArgs:  map[string]string{},
	}
	for _, kv := range q["buildarg"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			http.Error(w, "buildarg must be KEY=VALUE", http.StatusBadRequest)
			return
		}
		opts.BuildArgs[k] = v
	}

	dir, err := s.builder.ContextDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)
	// The context is unpacked like a layer: confined to dir, no device nodes,
	// ownership not applied (COPY sets it).
	if _, err := layer.Apply(r.Body, dir, layer.ApplyOptions{Mode: layer.ModeFlatten}); err != nil {
		http.Error(w, "build context: "+err.Error(), http.StatusBadRequest)
		return
	}
	opts.ContextDir = dir

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	out := &ndjsonWriter{enc: json.NewEncoder(w), fl: fl}
	opts.Out = out

	res, err := s.builder.Build(r.Context(), opts)
	if err != nil {
		s.log.Warn("build failed", "err", err)
		out.send(buildEvent{Error: err.Error()})
		return
	}
	s.log.Info("build finished", "image", res.ImageID, "tags", res.Tags)
	out.send(buildEvent{Done: true, ImageID: res.ImageID, Tags: res.Tags})
}
