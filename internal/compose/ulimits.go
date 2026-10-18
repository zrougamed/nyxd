package compose

import (
	"fmt"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// BundleRlimitsFromUlimits maps compose ulimits into OCI process.rlimits.
// Unknown names are rejected. An empty map returns nil (bundle keeps defaults).
func BundleRlimitsFromUlimits(ulimits map[string]Ulimit) ([]bundle.Rlimit, error) {
	if len(ulimits) == 0 {
		return nil, nil
	}
	out := make([]bundle.Rlimit, 0, len(ulimits))
	for name, u := range ulimits {
		typ, err := ulimitType(name)
		if err != nil {
			return nil, err
		}
		hard, soft := u.Hard, u.Soft
		if hard == 0 && soft == 0 {
			return nil, fmt.Errorf("ulimits.%s: soft and hard are both zero", name)
		}
		if hard == 0 {
			hard = soft
		}
		if soft == 0 {
			soft = hard
		}
		if soft > hard {
			return nil, fmt.Errorf("ulimits.%s: soft (%d) > hard (%d)", name, soft, hard)
		}
		out = append(out, bundle.Rlimit{Type: typ, Soft: soft, Hard: hard})
	}
	return out, nil
}

func ulimitType(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "nofile":
		return "RLIMIT_NOFILE", nil
	case "nproc":
		return "RLIMIT_NPROC", nil
	case "core":
		return "RLIMIT_CORE", nil
	case "memlock":
		return "RLIMIT_MEMLOCK", nil
	case "stack":
		return "RLIMIT_STACK", nil
	case "as":
		return "RLIMIT_AS", nil
	default:
		if strings.HasPrefix(strings.ToUpper(n), "RLIMIT_") {
			return strings.ToUpper(n), nil
		}
		return "", fmt.Errorf("ulimits: unsupported %q", name)
	}
}
