package control

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/zrougamed/nyxd/internal/supervisor"
)

var (
	errNoSuchContainerID    = errors.New("no matching container")
	errAmbiguousContainerID = errors.New("ambiguous container id")
)

// resolveContainerID maps a user-supplied id to the canonical supervisor id.
// Accepts exact canonical ids, unambiguous canonical prefixes, exact 12-char hex short ids
// ([supervisor.DisplayID]), or unambiguous hex prefixes of those short ids.
func (s *Server) resolveContainerID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("missing container id")
	}
	if s.sup == nil {
		return raw, nil
	}
	ids := s.sup.List()

	for _, id := range ids {
		if id == raw {
			return id, nil
		}
	}

	if len(raw) == 12 && isAllHexDigits(raw) {
		for _, id := range ids {
			if strings.EqualFold(supervisor.DisplayID(id), raw) {
				return id, nil
			}
		}
	}

	var canonPrefix []string
	for _, id := range ids {
		if strings.HasPrefix(id, raw) {
			canonPrefix = append(canonPrefix, id)
		}
	}
	switch len(canonPrefix) {
	case 1:
		return canonPrefix[0], nil
	case 0:
		// continue
	default:
		return "", fmt.Errorf("%w: %q matches %v", errAmbiguousContainerID, raw, canonPrefix)
	}

	if isAllHexDigits(raw) && len(raw) >= 1 && len(raw) <= 12 {
		rl := strings.ToLower(raw)
		var shortMatches []string
		for _, id := range ids {
			sid := strings.ToLower(supervisor.DisplayID(id))
			if strings.HasPrefix(sid, rl) {
				shortMatches = append(shortMatches, id)
			}
		}
		switch len(shortMatches) {
		case 1:
			return shortMatches[0], nil
		case 0:
			return "", fmt.Errorf("%w: %q", errNoSuchContainerID, raw)
		default:
			return "", fmt.Errorf("%w: %q matches short ids for %v", errAmbiguousContainerID, raw, shortMatches)
		}
	}

	return "", fmt.Errorf("%w: %q", errNoSuchContainerID, raw)
}

func isAllHexDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// safeLogContainerID rejects path injection for log filenames under dataDir/logs/.
func safeLogContainerID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if r == '.' || r == '/' || r == '\\' || unicode.IsSpace(r) {
			return false
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// resolveContainerIDForLogs resolves a supervised id like [Server.resolveContainerID], or —
// if the container already exited and was dropped from the supervisor (e.g. fast one-shot) —
// accepts raw when a log file dataDir/logs/<raw>.log already exists.
func (s *Server) resolveContainerIDForLogs(raw string) (string, error) {
	canon, err := s.resolveContainerID(raw)
	if err == nil {
		return canon, nil
	}
	if s.sup == nil || !errors.Is(err, errNoSuchContainerID) {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if !safeLogContainerID(raw) {
		return "", err
	}
	p := filepath.Join(s.dataDir, "logs", raw+".log")
	if _, stErr := os.Stat(p); stErr == nil {
		return raw, nil
	}
	return "", err
}

// containerInSupervisor reports whether id is currently tracked by the supervisor.
func (s *Server) containerInSupervisor(id string) bool {
	if s.sup == nil {
		return false
	}
	for _, x := range s.sup.List() {
		if x == id {
			return true
		}
	}
	return false
}

// writeResolveError maps resolveContainerID errors to HTTP responses.
func writeResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchContainerID):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, errAmbiguousContainerID):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
