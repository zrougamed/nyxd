package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Build is a service's build: section, in the short form (a context path) or
// the long form (context, dockerfile, args, target).
type Build struct {
	Context    string
	Dockerfile string            // relative to Context (default "Dockerfile")
	Args       map[string]string // args: as a mapping or a KEY=VALUE list
	Target     string
}

// UnmarshalYAML implements yaml.Unmarshaler for both the string and mapping forms.
func (b *Build) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		b.Context = value.Value
		return nil
	case yaml.MappingNode:
		var raw struct {
			Context    string    `yaml:"context"`
			Dockerfile string    `yaml:"dockerfile"`
			Args       yaml.Node `yaml:"args"`
			Target     string    `yaml:"target"`
		}
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("build: %w", err)
		}
		b.Context, b.Dockerfile, b.Target = raw.Context, raw.Dockerfile, raw.Target
		args, err := decodeBuildArgs(&raw.Args)
		if err != nil {
			return err
		}
		b.Args = args
		return nil
	default:
		return fmt.Errorf("build: expected a path or a mapping, got yaml kind %d", value.Kind)
	}
}

func decodeBuildArgs(n *yaml.Node) (map[string]string, error) {
	switch n.Kind {
	case 0:
		return nil, nil
	case yaml.MappingNode:
		var m map[string]*string
		if err := n.Decode(&m); err != nil {
			return nil, fmt.Errorf("build args: %w", err)
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			if v != nil {
				out[k] = *v
			} else if ev, ok := os.LookupEnv(k); ok {
				out[k] = ev // "KEY:" with no value takes it from the environment
			}
		}
		return out, nil
	case yaml.SequenceNode:
		var seq []string
		if err := n.Decode(&seq); err != nil {
			return nil, fmt.Errorf("build args: %w", err)
		}
		out := make(map[string]string, len(seq))
		for _, kv := range seq {
			if k, v, ok := strings.Cut(kv, "="); ok {
				out[k] = v
			} else if ev, ok := os.LookupEnv(kv); ok {
				out[kv] = ev
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("build args: expected a mapping or a list")
	}
}

// ContextDir is the build context resolved against the compose file's directory.
func (b *Build) ContextDir(composeDir string) (string, error) {
	c := strings.TrimSpace(b.Context)
	if c == "" {
		c = "."
	}
	if strings.Contains(c, "://") || strings.HasPrefix(c, "git@") {
		return "", fmt.Errorf("build context %q: only local directories are supported", c)
	}
	if !filepath.IsAbs(c) {
		c = filepath.Join(composeDir, c)
	}
	return filepath.Clean(c), nil
}

// DockerfilePath is the Dockerfile path; a relative dockerfile: is resolved
// against the context, as Compose does.
func (b *Build) DockerfilePath(contextDir string) string {
	df := strings.TrimSpace(b.Dockerfile)
	if df == "" {
		df = "Dockerfile"
	}
	if filepath.IsAbs(df) {
		return df
	}
	return filepath.Join(contextDir, df)
}

// BuildImageName is the image a service with build: and no image: produces,
// "<project>-<service>:latest" like Compose.
func BuildImageName(project, service string) string {
	return strings.ToLower(project+"-"+service) + ":latest"
}

// SetBuildImages gives every service with build: and no image: its default
// image name. Call it once the project name is known.
func (st *Stack) SetBuildImages(project string) {
	for name, svc := range st.Services {
		if svc.Build != nil && strings.TrimSpace(svc.Image) == "" {
			svc.Image = BuildImageName(project, name)
			st.Services[name] = svc
		}
	}
}

// BuildServices lists the services with a build: section, sorted by name.
func (st *Stack) BuildServices() []string {
	var out []string
	for name, svc := range st.Services {
		if svc.Build != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
