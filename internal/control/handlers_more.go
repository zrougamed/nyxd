package control

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/logs"
)

func (s *Server) handleContainerKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "missing container id", http.StatusBadRequest)
		return
	}
	canon, err := s.resolveContainerID(id)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	id = canon
	signal := "KILL"
	var body struct {
		Signal string `json:"signal"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 512)).Decode(&body)
	if sig := strings.TrimSpace(body.Signal); sig != "" {
		signal = strings.TrimPrefix(strings.TrimSpace(sig), "SIG")
	}
	s.log.Info("control API kill", "id", id, "signal", signal)
	if err := s.sup.Kill(r.Context(), id, signal); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			http.Error(w, msg, http.StatusNotFound)
			return
		}
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "signal": signal})
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "missing container id", http.StatusBadRequest)
		return
	}
	canon, err := s.resolveContainerIDForLogs(id)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	id = canon
	logPath := filepath.Join(s.dataDir, "logs", id+".log")
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 100000 {
			tail = n
		}
	}
	follow := r.URL.Query().Get("follow") == "1" || r.URL.Query().Get("follow") == "true"
	plain := r.URL.Query().Get("plain") != "0"

	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "no log file for this container", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fl, canFlush := w.(http.Flusher)

	lines := tailLines(string(data), tail)
	for _, ln := range lines {
		writeLogLine(w, []byte(ln), plain)
	}
	if canFlush {
		fl.Flush()
	}

	if !follow {
		return
	}

	offset := int64(len(data))
	// Fast path: workload already finished before follow began and the log file has not
	// grown since our ReadFile — close immediately instead of ~1s of idle polling (hyperfine).
	if st, err := os.Stat(logPath); err == nil && st.Size() == offset && !s.containerInSupervisor(id) {
		return
	}

	ctx := r.Context()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	// End follow when the container is no longer supervised and the log file has been
	// fully read for several ticks (covers exit while this request was in flight).
	idle := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			inSup := s.containerInSupervisor(id)
			st, err := os.Stat(logPath)
			if err != nil {
				if !inSup {
					idle++
					if idle >= 4 {
						return
					}
				} else {
					idle = 0
				}
				continue
			}
			if st.Size() < offset {
				offset = 0
			}
			if st.Size() > offset {
				f, err := os.Open(logPath)
				if err != nil {
					idle = 0
					continue
				}
				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					idle = 0
					continue
				}
				sc := bufio.NewScanner(f)
				for sc.Scan() {
					b := sc.Bytes()
					writeLogLine(w, append([]byte(nil), b...), plain)
					offset += int64(len(b)) + 1
					if canFlush {
						fl.Flush()
					}
				}
				f.Close()
				idle = 0
			}
			if !inSup && st.Size() <= offset {
				idle++
				if idle >= 4 {
					return
				}
			} else {
				idle = 0
			}
		}
	}
}

func tailLines(s string, n int) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if n > 0 && len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	return parts
}

func writeLogLine(w io.Writer, line []byte, plain bool) {
	if !plain {
		_, _ = w.Write(append(line, '\n'))
		return
	}
	var ent logs.Entry
	if json.Unmarshal(line, &ent) == nil {
		_, _ = fmt.Fprint(w, ent.Log)
		if !strings.HasSuffix(ent.Log, "\n") {
			_, _ = w.Write([]byte{'\n'})
		}
		return
	}
	_, _ = w.Write(append(line, '\n'))
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		http.Error(w, "image store not available", http.StatusServiceUnavailable)
		return
	}
	refs, err := s.store.ListImageRefs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"images": refs})
}

type imageRemoveRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) handleImagesRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		http.Error(w, "image store not available", http.StatusServiceUnavailable)
		return
	}
	var body imageRemoveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ref := strings.TrimSpace(body.Ref)
	if ref == "" {
		http.Error(w, "missing ref", http.StatusBadRequest)
		return
	}
	if err := s.store.RemoveImage(ref); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ref": ref})
}

type imagePruneRequest struct {
	DryRun bool `json:"dry_run"`
}

func (s *Server) handleImagesPrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		http.Error(w, "image store not available", http.StatusServiceUnavailable)
		return
	}
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	var body imagePruneRequest
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)

	keep := s.sup.ImageRefsInUse()
	removed, err := s.store.PruneImagesNotIn(keep, body.DryRun)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"dry_run": body.DryRun,
		"removed": removed,
		"in_use":  len(keep),
	})
}
