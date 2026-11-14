package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

// maxRestartGrace caps ?t= on restart; past this, ask for a stop and a start.
const maxRestartGrace = time.Hour

func writeLifecycleError(w http.ResponseWriter, err error) {
	msg := image.TrimUserMessage(err)
	switch {
	case errors.Is(err, supervisor.ErrNoContainer):
		http.Error(w, msg, http.StatusNotFound)
	case strings.Contains(err.Error(), "already running"):
		http.Error(w, msg, http.StatusConflict)
	default:
		http.Error(w, msg, http.StatusBadRequest)
	}
}

func (s *Server) handleContainerStart(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	id, err := s.resolveContainerID(r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	// The container outlives the request, like run.
	if err := s.sup.StartExisting(s.baseCtx, id); err != nil {
		writeLifecycleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

// restartGrace reads ?t=SECONDS (docker restart -t).
func restartGrace(r *http.Request) (time.Duration, error) {
	v := strings.TrimSpace(r.URL.Query().Get("t"))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || time.Duration(n)*time.Second > maxRestartGrace {
		return 0, fmt.Errorf("t: want seconds between 0 and %d, got %q", int(maxRestartGrace.Seconds()), v)
	}
	return time.Duration(n) * time.Second, nil
}

func (s *Server) handleContainerRestart(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	grace, err := restartGrace(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.resolveContainerID(r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	noWriteDeadline(w) // the stop alone may take the whole grace period
	if err := s.sup.Restart(s.baseCtx, id, grace); err != nil {
		writeLifecycleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

// composeServiceIDs returns the container ids of the requested services (all
// when none are named) in dependency order.
func (s *Server) composeServiceIDs(w http.ResponseWriter, r *http.Request) ([]string, map[string]string, bool) {
	var body composeProjectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, image.TrimUserMessage(err), http.StatusBadRequest)
		return nil, nil, false
	}
	st, _, project, err := s.loadComposeProject(body)
	if err != nil {
		http.Error(w, image.HumanizeComposeBuildError(err), http.StatusBadRequest)
		return nil, nil, false
	}
	for _, name := range body.Services {
		if _, ok := st.Services[name]; !ok {
			http.Error(w, fmt.Sprintf("unknown service %q", name), http.StatusBadRequest)
			return nil, nil, false
		}
	}
	var ids []string
	service := map[string]string{}
	for _, name := range compose.TopologicalOrder(st) {
		if len(body.Services) > 0 && !slices.Contains(body.Services, name) {
			continue
		}
		id := compose.ContainerID(project, name)
		ids = append(ids, id)
		service[id] = name
	}
	return ids, service, true
}

func (s *Server) handleComposeStart(w http.ResponseWriter, r *http.Request) {
	s.composeLifecycle(w, r, false)
}

func (s *Server) handleComposeRestart(w http.ResponseWriter, r *http.Request) {
	s.composeLifecycle(w, r, true)
}

// composeLifecycle starts (or restarts) stack services in dependency order.
// compose start leaves running services alone.
func (s *Server) composeLifecycle(w http.ResponseWriter, r *http.Request, restart bool) {
	if s.sup == nil {
		http.Error(w, "supervisor not available", http.StatusServiceUnavailable)
		return
	}
	ids, service, ok := s.composeServiceIDs(w, r)
	if !ok {
		return
	}
	noWriteDeadline(w)
	var done []string
	for _, id := range ids {
		var err error
		switch {
		case restart:
			err = s.sup.Restart(s.baseCtx, id, 0)
		case s.sup.Running(id):
			continue
		default:
			err = s.sup.StartExisting(s.baseCtx, id)
		}
		if errors.Is(err, supervisor.ErrNoContainer) {
			http.Error(w, fmt.Sprintf("service %q has no container (nyx compose up creates it)", service[id]), http.StatusNotFound)
			return
		}
		if err != nil {
			writeLifecycleError(w, fmt.Errorf("service %q: %w", service[id], err))
			return
		}
		done = append(done, id)
	}
	key := "started"
	if restart {
		key = "restarted"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, key: done})
}
