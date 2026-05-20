package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
)

// DisplayID returns a stable 12-character lowercase hex id for a canonical container id
// (Docker-style short id for ps / stop prefix resolution).
func DisplayID(canonicalID string) string {
	if canonicalID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(canonicalID))
	return hex.EncodeToString(sum[:6])
}
