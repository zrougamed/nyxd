package compose

import (
	"fmt"
	"os"
	"path/filepath"
)

// ParseFile reads compose YAML from path: merges .env from the file's directory with
// the process environment, performs ${VAR} substitution on the raw YAML, then parses.
func ParseFile(path string) (*Stack, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("compose path: %w", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}
	return ParseInterpolated(raw, filepath.Dir(abs))
}

// ParseInterpolated applies env + substitution then parses compose YAML.
func ParseInterpolated(raw []byte, composeDir string) (*Stack, error) {
	env := InterpEnvFromDir(composeDir)
	yamlText := Substitute(string(raw), env)
	return Parse([]byte(yamlText))
}
