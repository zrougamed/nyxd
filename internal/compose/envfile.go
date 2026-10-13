package compose

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EnvFile is one env_file: entry. Required defaults to true, as in Compose.
type EnvFile struct {
	Path     string
	Required bool
}

// EnvFiles is env_file: as a path, a list of paths, or a list of
// {path, required} mappings.
type EnvFiles []EnvFile

// UnmarshalYAML implements yaml.Unmarshaler for the three forms.
func (e *EnvFiles) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*e = EnvFiles{{Path: value.Value, Required: true}}
		return nil
	case yaml.SequenceNode:
		out := make(EnvFiles, 0, len(value.Content))
		for _, n := range value.Content {
			switch n.Kind {
			case yaml.ScalarNode:
				out = append(out, EnvFile{Path: n.Value, Required: true})
			case yaml.MappingNode:
				var long struct {
					Path     string `yaml:"path"`
					Required *bool  `yaml:"required"`
				}
				if err := n.Decode(&long); err != nil {
					return fmt.Errorf("env_file: %w", err)
				}
				f := EnvFile{Path: long.Path, Required: long.Required == nil || *long.Required}
				out = append(out, f)
			default:
				return fmt.Errorf("env_file: unexpected entry")
			}
		}
		*e = out
		return nil
	default:
		return fmt.Errorf("env_file: expected a path or a list")
	}
}

// ServiceEnv is a service's container environment: its env_file: entries in
// order (paths relative to the compose file), then environment:, which wins.
func ServiceEnv(svc *Service, composeDir string) (map[string]string, error) {
	env := map[string]string{}
	for _, f := range svc.EnvFile {
		p := f.Path
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(composeDir, p)
		}
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) && !f.Required {
				continue
			}
			return nil, fmt.Errorf("env_file %s: %w", f.Path, err)
		}
		if err := mergeDotEnvFile(env, p); err != nil {
			return nil, fmt.Errorf("env_file %s: %w", f.Path, err)
		}
	}
	for k, v := range svc.Environment {
		env[k] = v
	}
	return env, nil
}
