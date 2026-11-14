package compose

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// Tmpfs is the compose tmpfs: key, either one path or a list of them. Each
// entry is PATH[:OPTIONS], the same syntax as `nyx run --tmpfs`.
type Tmpfs []string

// UnmarshalYAML implements yaml.Unmarshaler for the string and list forms.
func (t *Tmpfs) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*t = Tmpfs{value.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return fmt.Errorf("tmpfs: %w", err)
		}
		*t = list
		return nil
	default:
		return fmt.Errorf("tmpfs: expected a path or a list of paths, got yaml kind %d", value.Kind)
	}
}

// tmpfsFlagPairs are the mount flags a tmpfs entry may switch on. The
// defaults (first of each pair) match Docker's: no setuid binaries, no device
// nodes and nothing executable unless the entry asks for it.
var tmpfsFlagPairs = [][2]string{{"nosuid", "suid"}, {"nodev", "dev"}, {"noexec", "exec"}}

// tmpfsValueOpts are the key=value options passed through to the kernel.
// Anything else is rejected so a socket client can't hand crun arbitrary
// mount data.
var tmpfsValueOpts = []string{"size", "mode", "uid", "gid", "nr_inodes"}

// TmpfsMount parses PATH[:OPTIONS] (e.g. "/tmp:size=64m,mode=1777") into an
// OCI tmpfs mount.
func TmpfsMount(entry string) (bundle.Mount, error) {
	entry = strings.TrimSpace(entry)
	dst, optStr, _ := strings.Cut(entry, ":")
	dst = strings.TrimSpace(dst)
	if !strings.HasPrefix(dst, "/") {
		return bundle.Mount{}, fmt.Errorf("tmpfs %q: path must be absolute", entry)
	}
	dst = path.Clean(dst)
	if dst == "/" {
		return bundle.Mount{}, fmt.Errorf("tmpfs %q: cannot mount over /", entry)
	}

	flags := map[string]string{} // pair default -> chosen flag
	for _, p := range tmpfsFlagPairs {
		flags[p[0]] = p[0]
	}
	ro := false
	var values []string
	for opt := range strings.SplitSeq(optStr, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		if key, _, ok := strings.Cut(opt, "="); ok {
			if !slices.Contains(tmpfsValueOpts, key) {
				return bundle.Mount{}, fmt.Errorf("tmpfs %q: unsupported option %q", entry, key)
			}
			values = append(values, opt)
			continue
		}
		switch opt {
		case "ro":
			ro = true
			continue
		case "rw":
			ro = false
			continue
		}
		matched := false
		for _, p := range tmpfsFlagPairs {
			if opt == p[0] || opt == p[1] {
				flags[p[0]] = opt
				matched = true
			}
		}
		if !matched {
			return bundle.Mount{}, fmt.Errorf("tmpfs %q: unsupported option %q", entry, opt)
		}
	}

	opts := make([]string, 0, len(tmpfsFlagPairs)+len(values)+1)
	for _, p := range tmpfsFlagPairs {
		opts = append(opts, flags[p[0]])
	}
	if ro {
		opts = append(opts, "ro")
	}
	opts = append(opts, values...)
	return bundle.Mount{Destination: dst, Type: "tmpfs", Source: "tmpfs", Options: opts}, nil
}

// TmpfsMounts parses every entry with [TmpfsMount].
func TmpfsMounts(entries []string) ([]bundle.Mount, error) {
	out := make([]bundle.Mount, 0, len(entries))
	for _, e := range entries {
		m, err := TmpfsMount(e)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
