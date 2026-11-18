package compose

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// Compose secrets and configs. A service's secrets are copied into a
// per-container directory on a tmpfs (the daemon's runtime directory) and
// bind-mounted read-only at /run/secrets/<name>: their contents never reach
// the container's overlay, the data directory or the SD card, and specs,
// supervisor state and nyx inspect only hold that path. Configs are
// bind-mounted read-only from their file.

// FileSource is a top-level secrets: or configs: entry. Only file: is
// supported; environment: and external: are warned about and skipped.
type FileSource struct {
	File        string `yaml:"file"`
	Environment string `yaml:"environment"`
	External    bool   `yaml:"external"`
	Name        string `yaml:"name"`
}

// FileRef is a service's use of a secret or config: the short form (the
// name) or the long one.
type FileRef struct {
	Source string
	Target string
	UID    *int
	GID    *int
	Mode   *uint32
}

// FileRefs is a services' secrets: or configs: list.
type FileRefs []FileRef

// UnmarshalYAML accepts names and {source, target, uid, gid, mode} maps.
func (r *FileRef) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.Source = strings.TrimSpace(n.Value)
		if r.Source == "" {
			return fmt.Errorf("empty secret/config name")
		}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("secret/config: want a name or a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i].Value, n.Content[i+1]
		switch k {
		case "source":
			r.Source = strings.TrimSpace(v.Value)
		case "target":
			r.Target = strings.TrimSpace(v.Value)
		case "uid", "gid":
			id, err := strconv.Atoi(strings.TrimSpace(v.Value))
			if err != nil || id < 0 {
				return fmt.Errorf("secret/config %s %q: want a number", k, v.Value)
			}
			if k == "uid" {
				r.UID = &id
			} else {
				r.GID = &id
			}
		case "mode":
			m, err := parseFileMode(v.Value)
			if err != nil {
				return err
			}
			r.Mode = &m
		default:
			return fmt.Errorf("secret/config: unknown key %q", k)
		}
	}
	if r.Source == "" {
		return fmt.Errorf("secret/config: source is required")
	}
	return nil
}

// parseFileMode reads a mode the way Compose files write it: 0440 and
// 0o440 are octal (YAML 1.2 would read 0440 as decimal), 288 decimal.
func parseFileMode(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	base := 10
	switch {
	case strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O"):
		s, base = s[2:], 8
	case len(s) > 1 && s[0] == '0':
		base = 8
	}
	m, err := strconv.ParseUint(s, base, 32)
	if err != nil || m > 0o7777 {
		return 0, fmt.Errorf("secret/config mode %q: want an octal mode such as 0440", s)
	}
	return uint32(m), nil
}

// SecretsWarnings lists secrets and configs a service uses that compose up
// skips (environment: and external: sources are not supported yet).
func SecretsWarnings(st *Stack) []string {
	var out []string
	check := func(kind string, defs map[string]FileSource, refs func(Service) FileRefs) {
		names := make([]string, 0, len(st.Services))
		for n := range st.Services {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, ref := range refs(st.Services[n]) {
				def, ok := defs[ref.Source]
				if !ok {
					continue
				}
				switch {
				case def.External:
					out = append(out, fmt.Sprintf("service %q: external %s %q is not supported yet (skipped)", n, kind, ref.Source))
				case def.Environment != "" && def.File == "":
					out = append(out, fmt.Sprintf("service %q: %s %q from environment: is not supported yet (skipped)", n, kind, ref.Source))
				}
			}
		}
	}
	check("secret", st.Secrets, func(s Service) FileRefs { return s.Secrets })
	check("config", st.Configs, func(s Service) FileRefs { return s.Configs })
	return out
}

// validateFileRefs checks that every secret and config a service uses is
// declared.
func validateFileRefs(name string, svc *Service, st *Stack) error {
	for _, ref := range svc.Secrets {
		if _, ok := st.Secrets[ref.Source]; !ok {
			return fmt.Errorf("service %q: unknown secret %q (declare it under top-level secrets:)", name, ref.Source)
		}
	}
	for _, ref := range svc.Configs {
		if _, ok := st.Configs[ref.Source]; !ok {
			return fmt.Errorf("service %q: unknown config %q (declare it under top-level configs:)", name, ref.Source)
		}
	}
	return nil
}

// secretTarget is where a secret appears: /run/secrets/<name>, or the
// long form's target (relative targets are under /run/secrets).
func secretTarget(ref FileRef) string {
	t := ref.Target
	if t == "" {
		t = ref.Source
	}
	if !strings.HasPrefix(t, "/") {
		t = "/run/secrets/" + t
	}
	return path.Clean(t)
}

// configTarget is where a config appears: /<name>, or the target.
func configTarget(ref FileRef) string {
	t := ref.Target
	if t == "" {
		t = ref.Source
	}
	if !strings.HasPrefix(t, "/") {
		t = "/" + t
	}
	return path.Clean(t)
}

// SecretsDir is a container's directory under the secrets root.
func SecretsDir(root, containerID string) string { return filepath.Join(root, containerID) }

// SecretMounts prepares the secrets and configs of one service: secrets
// (and configs with uid, gid or mode) are copied under secretsRoot/<id>/
// with their ownership and mode, configs otherwise bind their file. Copies
// are named after their content's hash, so a changed secret changes the
// spec and compose up recreates the service; copies no longer used are
// removed. Skipped sources (environment:, external:) are left out.
func SecretMounts(svc *Service, st *Stack, composeDir, secretsRoot, containerID string) ([]bundle.Mount, error) {
	if len(svc.Secrets) == 0 && len(svc.Configs) == 0 {
		if secretsRoot != "" {
			_ = os.RemoveAll(SecretsDir(secretsRoot, containerID))
		}
		return nil, nil
	}
	var mounts []bundle.Mount
	keep := map[string]bool{}
	dir := ""
	copyOne := func(kind string, ref FileRef, src string, defMode uint32, target string) error {
		if secretsRoot == "" {
			return fmt.Errorf("%s %q: the daemon has no runtime directory for secrets", kind, ref.Source)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("%s %q: %w", kind, ref.Source, err)
		}
		mode, uid, gid := defMode, 0, 0
		if ref.Mode != nil {
			mode = *ref.Mode
		}
		if ref.UID != nil {
			uid = *ref.UID
		}
		if ref.GID != nil {
			gid = *ref.GID
		}
		sum := sha256.Sum256(append(append([]byte(fmt.Sprintf("%o:%d:%d:", mode, uid, gid)), data...), []byte(target)...))
		name := sanitizePathPart(ref.Source) + "-" + hex.EncodeToString(sum[:6])
		if dir == "" {
			dir = SecretsDir(secretsRoot, containerID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		p := filepath.Join(dir, name)
		keep[name] = true
		if cur, err := os.ReadFile(p); err != nil || !bytes.Equal(cur, data) {
			tmp := p + ".tmp"
			if err := os.WriteFile(tmp, data, 0o600); err != nil {
				return err
			}
			// The daemon is root (in its user namespace when rootless): the
			// copy is already 0:0 unless the service asked for an owner.
			if ref.UID != nil || ref.GID != nil {
				if err := os.Chown(tmp, uid, gid); err != nil {
					_ = os.Remove(tmp)
					return fmt.Errorf("%s %q: chown %d:%d: %w", kind, ref.Source, uid, gid, err)
				}
			}
			if err := os.Chmod(tmp, os.FileMode(mode)); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			if err := os.Rename(tmp, p); err != nil {
				return err
			}
		}
		mounts = append(mounts, bundle.Mount{Type: "bind", Source: p, Destination: target, Options: []string{"rbind", "ro"}})
		return nil
	}
	for _, ref := range svc.Secrets {
		def := st.Secrets[ref.Source]
		if def.External || def.File == "" {
			continue
		}
		src, err := resolveBindSource(def.File, composeDir)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", ref.Source, err)
		}
		if err := copyOne("secret", ref, src, 0o400, secretTarget(ref)); err != nil {
			return nil, err
		}
	}
	for _, ref := range svc.Configs {
		def := st.Configs[ref.Source]
		if def.External || def.File == "" {
			continue
		}
		src, err := resolveBindSource(def.File, composeDir)
		if err != nil {
			return nil, fmt.Errorf("config %q: %w", ref.Source, err)
		}
		if ref.UID != nil || ref.GID != nil || ref.Mode != nil {
			if err := copyOne("config", ref, src, 0o444, configTarget(ref)); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("config %q: %w", ref.Source, err)
		}
		mounts = append(mounts, bundle.Mount{Type: "bind", Source: src, Destination: configTarget(ref), Options: []string{"rbind", "ro"}})
	}
	if dir != "" {
		if ents, err := os.ReadDir(dir); err == nil {
			for _, e := range ents {
				if !keep[e.Name()] {
					_ = os.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	return mounts, nil
}
