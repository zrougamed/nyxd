package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/image"
)

type tagRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// handleImageTag makes target refer to the same image as source.
func (s *Server) handleImageTag(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "image store not configured", http.StatusServiceUnavailable)
		return
	}
	var body tagRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Source) == "" || strings.TrimSpace(body.Target) == "" {
		http.Error(w, "source and target are required", http.StatusBadRequest)
		return
	}
	if err := s.store.Tag(body.Source, body.Target); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "source": body.Source, "target": body.Target})
}

// handleImagePush uploads a local image to its registry, streaming NDJSON
// progress (same event shape as pull) and a final {"phase":"done"} or
// {"phase":"error"} line.
func (s *Server) handleImagePush(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.store == nil {
		http.Error(w, "image store not configured", http.StatusServiceUnavailable)
		return
	}
	var body pullRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Ref) == "" {
		http.Error(w, "missing ref", http.StatusBadRequest)
		return
	}
	if _, _, err := s.store.LoadImageMeta(body.Ref); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	out := &ndjsonEvents{enc: json.NewEncoder(w)}
	out.fl, _ = w.(http.Flusher)

	digest, err := s.store.Push(r.Context(), body.Ref, pullAuthFromPullBody(&body), out.send)
	if err != nil {
		s.log.Warn("push failed", "ref", body.Ref, "err", err)
		out.send(image.PullEvent{Phase: "error", Message: image.HumanizePullError(err)})
		return
	}
	s.log.Info("image pushed", "ref", body.Ref, "digest", digest)
	out.send(image.PullEvent{Phase: "done", OK: true, RefOut: body.Ref, Digest: digest})
}

type ndjsonEvents struct {
	enc *json.Encoder
	fl  http.Flusher
}

func (n *ndjsonEvents) send(ev image.PullEvent) {
	_ = n.enc.Encode(ev)
	if n.fl != nil {
		n.fl.Flush()
	}
}

// handleImageSave streams the given refs (?ref=a&ref=b) as an image archive.
func (s *Server) handleImageSave(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.store == nil {
		http.Error(w, "image store not configured", http.StatusServiceUnavailable)
		return
	}
	refs := r.URL.Query()["ref"]
	if len(refs) == 0 {
		http.Error(w, "at least one ref is required", http.StatusBadRequest)
		return
	}
	for _, ref := range refs {
		if _, _, err := s.store.LoadImageMeta(ref); err != nil {
			http.Error(w, ref+": "+err.Error(), http.StatusNotFound)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-tar")
	if err := s.store.Save(w, refs); err != nil {
		// Headers are sent; the client sees a truncated archive.
		s.log.Warn("save failed", "refs", refs, "err", err)
	}
}

// handleImageLoad imports an image archive from the request body.
func (s *Server) handleImageLoad(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.store == nil {
		http.Error(w, "image store not configured", http.StatusServiceUnavailable)
		return
	}
	refs, err := s.store.Load(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("images loaded", "refs", refs)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "refs": refs})
}

// noWriteDeadline lifts the server WriteTimeout for long-running transfers
// and builds (the daemon socket is local; clients cancel by disconnecting).
func noWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}
