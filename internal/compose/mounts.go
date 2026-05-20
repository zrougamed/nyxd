package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// ServiceExtraMounts turns compose volume lines into OCI bind mounts (and creates
// host directories for named volumes under dataDir/volumes/<project>/<name>/).
func ServiceExtraMounts(svc *Service, stack *Stack, composeDir, project, dataDir string) ([]bundle.Mount, error) {
	var out []bundle.Mount
	for _, line := range svc.Volumes {
		m, err := parseVolumeMount(strings.TrimSpace(line), stack, composeDir, project, dataDir)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func parseVolumeMount(line string, stack *Stack, composeDir, project, dataDir string) (bundle.Mount, error) {
	if line == "" {
		return bundle.Mount{}, fmt.Errorf("empty volume line")
	}
	ro := false
	if after, ok := strings.CutSuffix(line, ":ro"); ok {
		line, ro = strings.TrimSpace(after), true
	} else if after, ok := strings.CutSuffix(line, ":rw"); ok {
		line = strings.TrimSpace(after)
	}

	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return bundle.Mount{}, fmt.Errorf("invalid volume %q (expected source:destination)", line)
	}

	src := parts[0]
	dest := parts[1]
	if len(parts) == 3 {
		mode := strings.ToLower(strings.TrimSpace(parts[2]))
		switch mode {
		case "ro":
			ro = true
		case "rw", "":
		default:
			return bundle.Mount{}, fmt.Errorf("unknown volume mode %q in %q", parts[2], line)
		}
	} else if len(parts) > 3 {
		return bundle.Mount{}, fmt.Errorf("invalid volume %q", line)
	}

	if !filepath.IsAbs(dest) {
		return bundle.Mount{}, fmt.Errorf("volume destination must be absolute, got %q", dest)
	}

	var host string
	if isNamedVolume(src, stack) {
		dir, err := ensureNamedVolumeDir(dataDir, project, src)
		if err != nil {
			return bundle.Mount{}, err
		}
		host = dir
	} else {
		abs, err := resolveBindSource(src, composeDir)
		if err != nil {
			return bundle.Mount{}, err
		}
		host = abs
	}

	opts := []string{"rbind"}
	if ro {
		opts = append(opts, "ro")
	} else {
		opts = append(opts, "rw")
	}

	return bundle.Mount{
		Type:        "bind",
		Source:      host,
		Destination: dest,
		Options:     opts,
	}, nil
}

func isNamedVolume(src string, stack *Stack) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	_, ok := stack.Volumes[src]
	return ok
}

func resolveBindSource(src, composeDir string) (string, error) {
	src = strings.TrimSpace(src)
	if filepath.IsAbs(src) {
		return filepath.Clean(src), nil
	}
	base, err := filepath.Abs(composeDir)
	if err != nil {
		return "", err
	}
	joined := filepath.Clean(filepath.Join(base, src))
	joinedAbs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	prefix := base + string(filepath.Separator)
	if joinedAbs == base || strings.HasPrefix(joinedAbs, prefix) {
		return joinedAbs, nil
	}
	return "", fmt.Errorf("relative bind %q escapes compose directory %q", src, composeDir)
}

// NamedVolumeHostPath is the host directory for a declared named volume
// (matches paths used by [ServiceExtraMounts]).
func NamedVolumeHostPath(dataDir, project, volName string) string {
	return filepath.Join(dataDir, "volumes", sanitizePathPart(project), sanitizePathPart(volName))
}

func ensureNamedVolumeDir(dataDir, project, volName string) (string, error) {
	p := NamedVolumeHostPath(dataDir, project, volName)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", fmt.Errorf("named volume %q: %w", volName, err)
	}
	return p, nil
}

func sanitizePathPart(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}

// EnvMapToSlice converts a service environment map to KEY=VAL lines with stable order.
func EnvMapToSlice(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
