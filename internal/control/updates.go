package control

import (
	"encoding/json"
	"net/http"

	"github.com/zrougamed/nyxd/internal/update"
)

// SetUpdater enables GET (check) and POST (update now) /v1/updates.
func (s *Server) SetUpdater(u *update.Updater) { s.updater = u }

// handleUpdates reports (GET) or applies (POST) image updates for the
// containers labelled nyx.update=digest.
func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	noWriteDeadline(w)
	if s.updater == nil {
		http.Error(w, "updates not available", http.StatusServiceUnavailable)
		return
	}
	var res []update.Result
	if r.Method == http.MethodPost {
		res = s.updater.Run(r.Context())
	} else {
		res = s.updater.Check(r.Context())
	}
	if res == nil {
		res = []update.Result{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"containers": res})
}
